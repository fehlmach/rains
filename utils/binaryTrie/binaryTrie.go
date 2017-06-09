package binaryTrie

import (
	"net"
	"rains/rainslib"
	"rains/utils/set"
	"time"
)

//Trie is a node of a binary trie.
type Trie struct {
	child      [2]*Trie
	assertions map[rainslib.ObjectType]*set.Set
	zones      *set.Set
}

//Find returns the most specific address assertion or zone in relation to the given netAddress' prefix.
//If no address assertion or zone is found it return false
func (t *Trie) Find(netAddr *net.IPNet, types []rainslib.ObjectType, depth int) (*rainslib.AddressAssertionSection, *rainslib.AddressZoneSection, bool) {
	addrmasks := [8]byte{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01}
	prfLength, _ := netAddr.Mask.Size()

	if depth < prfLength {
		var childidx int
		if netAddr.IP[depth/8]&addrmasks[depth%8] == 0 {
			childidx = 0
		} else {
			childidx = 1
		}

		if t.child[childidx] == nil {
			return containedElement(t, types)
		}

		if a, z, ok := t.child[childidx].Find(netAddr, types, depth+1); ok {
			return a, z, ok
		}
	}
	return containedElement(t, types)
}

//containedElement returns true and the first addressAssertion that matches one of the given types or if none is found the first addressZone (if present).
//in case there is neither false is returned
func containedElement(t *Trie, types []rainslib.ObjectType) (*rainslib.AddressAssertionSection, *rainslib.AddressZoneSection, bool) {
	for _, obj := range types {
		aSet := t.assertions[obj]
		if aSet.Len() > 0 {
			return aSet.GetAll()[0].(*rainslib.AddressAssertionSection), nil, true
		}
	}
	if t.zones.Len() > 0 {
		return nil, t.zones.GetAll()[0].(*rainslib.AddressZoneSection), true
	}
	return nil, nil, false
}

//AddAssertion adds the given address assertion to the map (keyed by objectType) at the trie node corresponding to the network address.
func (t *Trie) AddAssertion(assertion *rainslib.AddressAssertionSection) {
	node := getNode(t, assertion.SubjectAddr, 0)
	for _, obj := range assertion.Content {
		node.assertions[obj.Type].Add(assertion)
	}
}

//AddZone adds the given address zone to the list of address zones at the trie node corresponding to the network address.
func (t *Trie) AddZone(zone *rainslib.AddressZoneSection) {
	node := getNode(t, zone.SubjectAddr, 0)
	node.zones.Add(zone)
}

func getNode(t *Trie, ipNet *net.IPNet, depth int) *Trie {
	addrmasks := [8]byte{0x80, 0x40, 0x20, 0x10, 0x08, 0x04, 0x02, 0x01}
	prfLength, _ := ipNet.Mask.Size()

	var subidx int
	if depth < prfLength {
		if ipNet.IP[depth/8]&addrmasks[depth%8] == 0 {
			subidx = 0
		} else {
			subidx = 1
		}

		if t.child[subidx] == nil {
			t.child[subidx] = &Trie{assertions: make(map[rainslib.ObjectType]*set.Set)}
			t.child[subidx].zones = set.New()
			t.child[subidx].assertions[rainslib.OTName] = set.New()
			t.child[subidx].assertions[rainslib.OTRedirection] = set.New()
			t.child[subidx].assertions[rainslib.OTRegistrant] = set.New()
			t.child[subidx].assertions[rainslib.OTDelegation] = set.New()

		}
		return getNode(t.child[subidx], ipNet, depth+1)
	}
	return t
}

//DeleteExpiredElements removes all expired elements from the trie. The trie structure is not updated when a node gets empty
func (t *Trie) DeleteExpiredElements() {
	for _, s := range t.assertions {
		assertions := s.GetAll()
		for _, a := range assertions {
			if a.(*rainslib.AddressAssertionSection).ValidUntil() < time.Now().Unix() {
				s.Delete(a)
			}
		}
	}
	zones := t.zones.GetAll()
	for _, zone := range zones {
		if zone.(*rainslib.AddressZoneSection).ValidUntil() < time.Now().Unix() {
			t.zones.Delete(zone)
		}
	}
}
