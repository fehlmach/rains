# rainspub Design

`rainspub` takes input in the form of RAINS zonefiles (see 
[the zonefile definition](../DESIGN-NOTES.md#zonefiles)) and a keypair for a zone, 
generates signed assertions, shards, and zones, and publishes these to a set 
of RAINS servers using the RAINS protocol.

Its design is fairly simple, based around a linear workflow:

- take as input a keypair, a zonefile, a set of server addresses, and sharding
  and validity parameters
- parse the zonefile into a set of unsigned assertions
- group those assertions into shards based on a set of sharding parameters
- sign assertions, shards, and zones with a validity specified by validity parameters
- connect to the specified servers and push the signed messages to them using the RAINS protocol

an authority server (in the traditional, DNS-like sense) is therefore
constructed by running `rainspub` and `rainsd` at the same time, with
`rainspub` pushing only to the colocated `rainsd`.