package rainsd

import (
	"fmt"
	"math"
	"strings"
	"time"

	log "github.com/inconshreveable/log15"
	"github.com/netsec-ethz/rains/internal/pkg/keys"
	"github.com/netsec-ethz/rains/internal/pkg/message"
	"github.com/netsec-ethz/rains/internal/pkg/object"
	"github.com/netsec-ethz/rains/internal/pkg/query"
	"github.com/netsec-ethz/rains/internal/pkg/section"
	"github.com/netsec-ethz/rains/internal/pkg/siglib"
	"github.com/netsec-ethz/rains/internal/pkg/signature"
	"github.com/netsec-ethz/rains/internal/pkg/token"
	"github.com/netsec-ethz/rains/internal/pkg/util"
)

//verify verifies msgSender.Section
//It checks the consistency of the msgSender.Section and if it is inconsistent a notification msg is sent. (Consistency with cached elements is checked later in engine)
//It validates all signatures (including contained once), stripping of expired once.
//If no signature remains on an assertion, shard, zone, addressAssertion or addressZone it gets dropped (signatures of contained sections are not taken into account).
//If there happens an error in the signature verification process of any signature, the whole section gets dropped (signatures of contained sections are also considered)
func (s *Server) verify(msgSender msgSectionSender) {
	log.Info(fmt.Sprintf("Verify %T", msgSender.Section), "server", s.Addr(), "msgSectionSender", msgSender)
	switch msgSender.Section.(type) {
	case *section.Assertion, *section.Shard, *section.Pshard, *section.Zone:
		sectionSender := sectionWithSigSender{
			Section: msgSender.Section.(section.WithSigForward),
			Sender:  msgSender.Sender,
			Token:   msgSender.Token,
		}
		verifySection(sectionSender, s)
	case *query.Name:
		verifyQuery(msgSender.Section.(section.Query), msgSender, s)
	default:
		log.Warn("Not supported Msg section to verify", "msgSection", msgSender)
	}
}

//verifySection forwards the received section protected by signature(s) to be processed if it is
//consistent, all nonexpired signatures verify and there is at least one non expired signature.
func verifySection(sectionSender sectionWithSigSender, s *Server) {
	if !sectionSender.Section.IsConsistent() {
		sendNotificationMsg(sectionSender.Token, sectionSender.Sender, section.NTRcvInconsistentMsg,
			"contained section has context or subjectZone", s)
		return //already logged, that contained section is invalid
	}
	if contextInvalid(sectionSender.Section.GetContext()) {
		sendNotificationMsg(sectionSender.Token, sectionSender.Sender, section.NTRcvInconsistentMsg,
			"invalid context", s)
		return //already logged, that context is invalid
	}
	if verifySignatures(sectionSender, s) {
		sectionSender.Section.AddSigInMarshaller()
		s.assert(sectionSender, s.authority[zoneContext{
			Zone:    sectionSender.Section.GetSubjectZone(),
			Context: sectionSender.Section.GetContext(),
		}])
	}
}

//verifyQuery forwards the received query to be processed if it is consistent and not expired.
func verifyQuery(query section.Query, msgSender msgSectionSender, s *Server) {
	if contextInvalid(query.GetContext()) {
		sendNotificationMsg(msgSender.Token, msgSender.Sender, section.NTRcvInconsistentMsg,
			"invalid context", s)
		return //already logged, that context is invalid
	}
	if !isQueryExpired(query.GetExpiration()) {
		s.processQuery(msgSender)
	}
}

//contextInvalid return true if it is not the global context and the context does not contain a context marker '-cx'.
func contextInvalid(context string) bool {
	if context != "." && !strings.Contains(context, "cx-") {
		log.Warn("Context is malformed.", "context", context)
		return true
	}
	return false
}

//isQueryExpired returns true if the query has expired
func isQueryExpired(expires int64) bool {
	if expires < time.Now().Unix() {
		log.Warn("Query expired", "expirationTime", expires, "now", time.Now().Unix())
		return true
	}
	log.Debug("Query is not expired")
	return false
}

//verifySignatures verifies all signatures of sectionSender.Section and strips off expired
//signatures. If a public key is missing a query is issued and the section is added to the pending
//key cache. It returns false if there is no signature left on the message or when at least one
//public keys is missing.
func verifySignatures(sectionSender sectionWithSigSender, s *Server) bool {
	section := sectionSender.Section
	keysNeeded := make(map[signature.MetaData]bool)
	section.NeededKeys(keysNeeded)
	log.Debug("verifySignatures", "KeysNeeded", keysNeeded)
	publicKeys, missingKeys, ok := publicKeysPresent(section.GetSubjectZone(), section.GetContext(),
		keysNeeded, s.caches.ZoneKeyCache)
	if ok {
		log.Info("All public keys are present.", "msgSectionWithSig", section)
		addZoneAndContextToContainedSections(section)
		section.DontAddSigInMarshaller()
		return validSignature(section, publicKeys, s.config.MaxCacheValidity)
	}
	handleMissingKeys(sectionSender, missingKeys, s)
	return false
}

//publicKeysPresent returns true if all public keys are already cached for sigs.
//It also returns the set of cached publicKeys and a set of the missing publicKey identifiers
func publicKeysPresent(zone, context string, sigMetaData map[signature.MetaData]bool,
	zoneKeyCache zonePublicKeyCache) (
	map[keys.PublicKeyID][]keys.PublicKey, map[signature.MetaData]bool, bool) {
	keys := make(map[keys.PublicKeyID][]keys.PublicKey)
	missingKeys := make(map[signature.MetaData]bool)

	for sigData := range sigMetaData {
		if key, _, ok := zoneKeyCache.Get(zone, context, sigData); ok {
			//returned public key is guaranteed to be valid
			log.Debug("Corresponding Public key in cache.", "cacheKey=sigMetaData", sigData, "publicKey", key)
			keys[sigData.PublicKeyID] = append(keys[sigData.PublicKeyID], key)
		} else {
			log.Debug("Public key not in zoneKeyCache", "zone", zone, "cacheKey=sigMetaData", sigData)
			missingKeys[sigData] = true
		}
	}
	return keys, missingKeys, len(missingKeys) == 0
}

//addZoneAndContextToContainedSections adds subjectZone and context to all contained section.
func addZoneAndContextToContainedSections(sec section.WithSig) {
	if shard, ok := sec.(*section.Shard); ok {
		shard.AddCtxAndZoneToContent()
	}
	if zone, ok := sec.(*section.Zone); ok {
		zone.AddCtxAndZoneToContent()
	}
}

//validSignature validates section's signatures and strips all expired signatures away. Returns
//false if there are no signatures left (not considering internal sections) or if at least one
//signature is invalid (due to incorrect signature)
func validSignature(sec section.WithSigForward, keys map[keys.PublicKeyID][]keys.PublicKey,
	maxValidity util.MaxCacheValidity) bool {
	switch sec := sec.(type) {
	case *section.Assertion, *section.Pshard:
		return validateSignatures(sec, keys, maxValidity)
	case *section.Shard:
		return validShardSignatures(sec, keys, maxValidity)
	case *section.Zone:
		return validZoneSignatures(sec, keys, maxValidity)
	default:
		log.Warn("Not supported Msg Section")
		return false
	}
}

//validShardSignatures validates all signatures on the shard and contained assertions. It returns
//false if there is a signatures that does not verify. It removes the context and subjectZone of all
//contained assertions (which were necessary for signature verification)
func validShardSignatures(shard *section.Shard, keys map[keys.PublicKeyID][]keys.PublicKey,
	maxValidity util.MaxCacheValidity) bool {
	if !validateSignatures(shard, keys, maxValidity) ||
		!validContainedAssertions(shard.Content, keys, maxValidity) {
		return false
	}
	return true
}

//validZoneSignatures validates all signatures on the zone and contained assertions and shards. It
//returns false if there is a signatures that does not verify. It removes the subjectZone and
//context of all contained assertions and shards (which were necessary for signature verification)
func validZoneSignatures(zone *section.Zone, keys map[keys.PublicKeyID][]keys.PublicKey,
	maxValidity util.MaxCacheValidity) bool {
	if !validateSignatures(zone, keys, maxValidity) {
		return false
	}
	for _, s := range zone.Content {
		if !validContainedAssertions([]*section.Assertion{s}, keys, maxValidity) {
			return false
		}
	}
	return true
}

//validContainedAssertions validates all signatures on assertions. It returns false if there is a
//signature that does not verify. It removes the subjectZone and context of all contained assertions
//(which were necessary for signature verification)
func validContainedAssertions(assertions []*section.Assertion,
	keys map[keys.PublicKeyID][]keys.PublicKey, maxValidity util.MaxCacheValidity) bool {
	for _, assertion := range assertions {
		if !siglib.CheckSectionSignatures(assertion, keys, maxValidity) {
			return false
		}
	}
	return true
}

//handleMissingKeys adds sectionSender to the pending key cache and sends a delegation query if
//necessary
func handleMissingKeys(sectionSender sectionWithSigSender, missingKeys map[signature.MetaData]bool, s *Server) {
	sec := sectionSender.Section
	log.Info("Some public keys are missing. Add section to pending signature cache",
		"#missingKeys", len(missingKeys), "section", sec)
	exp := getQueryValidity(sec.Sigs(keys.RainsKeySpace), s.config.DelegationQueryValidity)
	for k := range missingKeys {
		log.Info("MissingKeys", "key", k)
		if sendQuery := s.caches.PendingKeys.Add(sectionSender, k.Algorithm, k.KeyPhase); sendQuery {
			token := token.New()
			if ok := s.caches.PendingKeys.AddToken(token, exp, sectionSender.Sender,
				sec.GetSubjectZone(), sec.GetContext()); ok {
				query := &query.Name{
					Name:       sec.GetSubjectZone(),
					Context:    sec.GetContext(),
					Expiration: exp,
					Types:      []object.Type{object.OTDelegation},
				}
				msg := message.Message{Token: token, Content: []section.Section{query}}
				s.sendToRecursiveResolver(msg)
				continue
			}
		}
		log.Info("Already issued a delegation query for this context and zone.",
			"zone", sec.GetSubjectZone(), "context", sec.GetContext())
	}
}

//getQueryValidity returns the expiration value for a delegation query. It is either a configured
//upper bound or if smaller the longest validity time of all present signatures.
func getQueryValidity(sigs []signature.Sig, delegQValidity time.Duration) (validity int64) {
	for _, sig := range sigs {
		if sig.ValidUntil > validity {
			validity = sig.ValidUntil
		}
	}
	//upper bound the validity time
	upperBound := time.Now().Add(delegQValidity).Unix()
	if validity > upperBound {
		validity = upperBound
	}
	return validity
}

//validateSignatures returns true if all non expired signatures of section are valid and there is at
//least one signature valid before Config.MaxValidity. It removes valid signatures that are expired
func validateSignatures(section section.WithSigForward, keyMap map[keys.PublicKeyID][]keys.PublicKey, maxValidity util.MaxCacheValidity) bool {
	if !siglib.CheckSectionSignatures(section, keyMap, maxValidity) {
		return false //already logged
	}
	if section.ValidSince() == math.MaxInt64 {
		log.Info("No signature is valid before the MaxValidity date in the future.")
		return false
	}
	return len(section.Sigs(keys.RainsKeySpace)) > 0
}
