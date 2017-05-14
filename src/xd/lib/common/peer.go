package common

import (
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"xd/lib/network"
	"xd/lib/network/i2p"
	"xd/lib/version"
)

// PeerID is a buffer for bittorrent peerid
type PeerID [20]byte

// Bytes gets buffer as byteslice
func (id PeerID) Bytes() []byte {
	return id[:]
}

// GeneratePeerID generates a new peer id for XD
func GeneratePeerID() (id PeerID) {
	io.ReadFull(rand.Reader, id[:])
	id[0] = '-'
	v := version.Name + version.Major + version.Minor + version.Patch + "0-"
	copy(id[1:], []byte(v[:]))
	return
}

// encode to string
func (id PeerID) String() string {
	return url.QueryEscape(string(id.Bytes()))
}

// Peer provides info for a bittorrent swarm peer
type Peer struct {
	Compact i2p.Base32Addr `bencode:"-"`
	IP      string         `bencode:"ip"`
	Port    int            `bencode:"port"`
	ID      PeerID         `bencode:"id"`
}

// Resolve resolves network address of peer
func (p *Peer) Resolve(n network.Network) (a net.Addr, err error) {
	if len(p.IP) > 0 {
		// prefer ip
		parts := strings.Split(p.IP, ".i2p")
		a = i2p.I2PAddr(parts[0])
	} else {
		// try compact
		a, err = n.Lookup(p.Compact.String(), fmt.Sprintf("%d", p.Port))
	}
	return
}
