package swarm

import (
	"net"
	"sync"
	"time"
	"xd/lib/bittorrent"
	"xd/lib/bittorrent/extensions"
	"xd/lib/common"
	"xd/lib/log"
	"xd/lib/util"
)

const DefaultMaxParallelRequests = 2

// a peer connection
type PeerConn struct {
	inbound             bool
	closing             bool
	c                   net.Conn
	id                  common.PeerID
	t                   *Torrent
	sendMtx             sync.Mutex
	bf                  *bittorrent.Bitfield
	peerChoke           bool
	peerInterested      bool
	usChoke             bool
	usInterested        bool
	Done                func()
	lastSend            time.Time
	tx                  util.Rate
	lastRecv            time.Time
	rx                  util.Rate
	downloading         []*common.PieceRequest
	ourOpts             *extensions.Message
	theirOpts           *extensions.Message
	MaxParalellRequests int
	access              sync.Mutex
}

// get stats for this connection
func (c *PeerConn) Stats() (st *PeerConnStats) {
	st = new(PeerConnStats)
	st.TX = c.tx.Rate()
	st.RX = c.rx.Rate()
	st.Addr = c.c.RemoteAddr().String()
	st.ID = c.id.String()
	return
}

func makePeerConn(c net.Conn, t *Torrent, id common.PeerID, ourOpts *extensions.Message) *PeerConn {
	p := new(PeerConn)
	p.c = c
	p.t = t
	p.ourOpts = ourOpts
	p.peerChoke = true
	p.usChoke = true
	p.usInterested = true
	copy(p.id[:], id[:])
	p.MaxParalellRequests = t.MaxRequests
	p.downloading = []*common.PieceRequest{}
	return p
}

func (c *PeerConn) start() {
	go c.runReader()
	go c.runKeepAlive()
	go c.tickStats()
}

func (c *PeerConn) runKeepAlive() {
	for !c.closing {
		time.Sleep(time.Second)
		c.sendKeepAlive()
	}
}

func (c *PeerConn) tickStats() {
	for !c.closing {
		time.Sleep(time.Second)
		c.tx.Tick()
		c.rx.Tick()
	}
}

func (c *PeerConn) doSend(msg *common.WireMessage) {
	if !c.closing && msg != nil {
		c.sendMtx.Lock()
		now := time.Now()
		c.lastSend = now
		if c.RemoteChoking() && msg.MessageID() == common.Request {
			// drop
			log.Debugf("drop request because choke")
			r := msg.GetPieceRequest()
			c.cancelDownload(r)
		} else {
			log.Debugf("writing %d bytes", msg.Len())
			err := msg.Send(c.c)
			if err == nil {
				if msg.MessageID() == common.Piece {
					c.tx.AddSample(uint64(msg.Len()))
				}
				log.Debugf("wrote message %s %d bytes", msg.MessageID(), msg.Len())
			} else {
				log.Debugf("write error: %s", err.Error())
			}
		}
		c.sendMtx.Unlock()
	}
}

// queue a send of a bittorrent wire message to this peer
func (c *PeerConn) Send(msg *common.WireMessage) {
	go c.doSend(msg)
}

func (c *PeerConn) recv(msg *common.WireMessage) (err error) {
	c.lastRecv = time.Now()
	if (!msg.KeepAlive()) && msg.MessageID() == common.Piece {
		c.rx.AddSample(uint64(msg.Len()))
	}
	log.Debugf("got %d bytes from %s", msg.Len(), c.id)
	err = c.inboundMessage(msg)
	return
}

// send choke
func (c *PeerConn) Choke() {
	if c.usChoke {
		log.Warnf("multiple chokes sent to %s", c.id.String())
	} else {
		log.Debugf("choke peer %s", c.id.String())
		c.Send(common.NewWireMessage(common.Choke, nil))
		c.usChoke = true
	}
}

// send unchoke
func (c *PeerConn) Unchoke() {
	if c.usChoke {
		log.Debugf("unchoke peer %s", c.id.String())
		c.Send(common.NewWireMessage(common.UnChoke, nil))
		c.usChoke = false
	}
}

func (c *PeerConn) gotDownload(p *common.PieceData) {
	c.access.Lock()
	var downloading []*common.PieceRequest
	for _, r := range c.downloading {
		if r.Matches(p) {
			c.t.pt.handlePieceData(p)
		} else {
			downloading = append(downloading, r)
		}
	}
	c.downloading = downloading
	c.access.Unlock()
}

func (c *PeerConn) cancelDownload(req *common.PieceRequest) {
	c.access.Lock()
	var downloading []*common.PieceRequest
	for _, r := range c.downloading {
		if r.Equals(req) {
			c.t.pt.canceledRequest(r)
		} else {
			downloading = append(downloading, r)
		}
	}
	c.downloading = downloading
	c.access.Unlock()
}

func (c *PeerConn) numDownloading() int {
	c.access.Lock()
	i := len(c.downloading)
	c.access.Unlock()
	return i
}

func (c *PeerConn) queueDownload(req *common.PieceRequest) {
	if c.closing {
		c.clearDownloading()
		return
	}
	c.access.Lock()
	c.downloading = append(c.downloading, req)
	log.Debugf("ask %s for %d %d %d", c.id.String(), req.Index, req.Begin, req.Length)
	c.Send(req.ToWireMessage())
	c.access.Unlock()
}

func (c *PeerConn) clearDownloading() {
	c.access.Lock()
	for _, r := range c.downloading {
		c.t.pt.canceledRequest(r)
	}
	c.downloading = []*common.PieceRequest{}
	c.access.Unlock()
}

// returns true if the remote peer has piece with given index
func (c *PeerConn) HasPiece(piece uint32) bool {
	if c.bf == nil {
		// no bitfield
		return false
	}
	return c.bf.Has(piece)
}

// return true if this peer is choking us otherwise return false
func (c *PeerConn) RemoteChoking() bool {
	return c.peerChoke
}

// return true if we are choking the remote peer otherwise return false
func (c *PeerConn) Chocking() bool {
	return c.usChoke
}

func (c *PeerConn) remoteUnchoke() {
	if !c.peerChoke {
		log.Warnf("remote peer %s sent multiple unchokes", c.id.String())
	}
	c.peerChoke = false
	log.Debugf("%s unchoked us", c.id.String())
}

func (c *PeerConn) remoteChoke() {
	if c.peerChoke {
		log.Warnf("remote peer %s sent multiple chokes", c.id.String())
	}
	c.peerChoke = true
	log.Debugf("%s choked us", c.id.String())
}

func (c *PeerConn) markInterested() {
	c.peerInterested = true
	log.Debugf("%s is interested", c.id.String())
}

func (c *PeerConn) markNotInterested() {
	c.peerInterested = false
	log.Debugf("%s is not interested", c.id.String())
}

// hard close connection
func (c *PeerConn) Close() {
	if c.closing {
		return
	}
	c.closing = true
	for _, r := range c.downloading {
		c.t.pt.canceledRequest(r)
	}
	c.downloading = nil
	log.Debugf("%s closing connection", c.id.String())
	c.c.Close()
	if c.inbound {
		c.t.removeIBConn(c)
	} else {
		c.t.removeOBConn(c)
	}
}

// run read loop
func (c *PeerConn) runReader() {
	err := common.ReadWireMessages(c.c, c.recv)
	if err != nil {
		log.Debugf("PeerConn() reader failed: %s", err.Error())
	}
	c.Close()
}

func (c *PeerConn) checkInterested() {
	bf := c.t.Bitfield()
	if c.bf.XOR(bf).CountSet() > 0 {
		c.usInterested = true
		m := common.NewInterested()
		c.Send(m)
	} else {
		c.usInterested = false
		m := common.NewNotInterested()
		c.Send(m)
	}
}

func (c *PeerConn) inboundMessage(msg *common.WireMessage) (err error) {

	if msg.KeepAlive() {
		log.Debugf("keepalive from %s", c.id)
		return
	}
	msgid := msg.MessageID()
	log.Debugf("%s from %s", msgid.String(), c.id.String())
	if msgid == common.BitField {
		isnew := false
		if c.bf == nil {
			isnew = true
		}
		c.bf = bittorrent.NewBitfield(c.t.MetaInfo().Info.NumPieces(), msg.Payload())
		log.Debugf("got bitfield from %s", c.id.String())
		c.checkInterested()
		if isnew {
			c.Unchoke()
			if c.ourOpts != nil {
				c.Send(c.ourOpts.ToWireMessage())
			}
			go c.runDownload()
		}
		return
	}
	if msgid == common.Choke {
		c.remoteChoke()
		for _, r := range c.downloading {
			c.t.pt.canceledRequest(r)
		}
	}
	if msgid == common.UnChoke {
		c.remoteUnchoke()
	}
	if msgid == common.Interested {
		c.markInterested()
	}
	if msgid == common.NotInterested {
		c.markNotInterested()
	}
	if msgid == common.Request {
		ev := msg.GetPieceRequest()
		c.t.handlePieceRequest(c, ev)
	}
	if msgid == common.Piece {
		d := msg.GetPieceData()
		if d == nil {
			log.Warnf("invalid piece data message from %s", c.id.String())
			c.Close()
		} else {
			c.gotDownload(d)
		}
	}

	if msgid == common.Have {
		// update bitfield
		idx := msg.GetHave()
		if c.bf != nil {
			c.bf.Set(idx)
			c.checkInterested()
		} else {
			// default to interested if we have no bitfield yet
			c.Send(common.NewInterested())
		}
	}
	if msgid == common.Cancel {
		// TODO: check validity
		r := msg.GetPieceRequest()
		c.t.pt.canceledRequest(r)
	}
	if msgid == common.Extended {
		// handle extended options
		opts := extensions.FromWireMessage(msg)
		if opts == nil {
			log.Warnf("failed to parse extended options for %s", c.id.String())
		} else {
			c.handleExtendedOpts(opts)
		}
	}
	return
}

// handles an inbound pex message
func (c *PeerConn) handlePEX(m interface{}) {

	pex, ok := m.(map[string]interface{})
	if ok {
		var added interface{}
		added, ok = pex["added"]
		if ok {
			c.handlePEXAdded(added)
		}
		added, ok = pex["added.f"]
		if ok {
			c.handlePEXAddedf(added)
		}
	} else {
		log.Errorf("invalid pex message: %q", m)
	}
}

// handle inbound PEX message payload
func (c *PeerConn) handlePEXAdded(m interface{}) {
	var peers []common.Peer
	msg := m.(string)
	l := len(msg) / 32
	for l > 0 {
		var p common.Peer
		// TODO: bounds check
		copy(p.Compact[:], msg[(l-1)*32:l*32])
		l--
		peers = append(peers, p)
	}
	c.t.addPeers(peers)
}

func (c *PeerConn) handlePEXAddedf(m interface{}) {
	// TODO: implement this
}

func (c *PeerConn) SupportsPEX() bool {
	if c.theirOpts == nil {
		return false
	}
	return c.theirOpts.PEX()
}

func (c *PeerConn) sendPEX(connected, disconnected []byte) {
	id := c.theirOpts.Extensions[extensions.PeerExchange.String()]
	msg := extensions.NewPEX(id, connected, disconnected)
	c.Send(msg.ToWireMessage())
}

func (c *PeerConn) handleExtendedOpts(opts *extensions.Message) {
	log.Debugf("got extended opts from %s: %s", c.id.String(), opts)
	if opts.ID == 0 {
		// handshake
		if c.theirOpts == nil {
			c.theirOpts = opts.Copy()
		} else {
			log.Warnf("got multiple extended option handshakes from %s", c.id.String())
		}
	} else {
		// extended data
		if c.theirOpts == nil {
			log.Warnf("%s gave unexpected extended message %d", c.id.String(), opts.ID)
		} else {
			// lookup the extension number
			ext, ok := c.theirOpts.Lookup(opts.ID)
			if ok {
				if ext == extensions.PeerExchange.String() {
					// this is PEX message
					c.handlePEX(opts.Payload)
				} else if ext == extensions.XDHT.String() {
					// xdht message
					err := c.t.xdht.HandleMessage(opts, c.id)
					if err != nil {
						log.Warnf("error handling xdht message from %s: %s", c.id.String(), err.Error())
					}
				}
			} else {
				log.Warnf("peer %s gave us extension for message we do not have id=%d", c.id.String(), opts.ID)
			}
		}
	}
}

func (c *PeerConn) sendKeepAlive() {
	tm := time.Now().Add(0 - (time.Minute * 2))
	if c.lastSend.Before(tm) {
		log.Debugf("send keepalive to %s", c.id.String())
		c.doSend(common.KeepAlive())
	}
}

// run download loop
func (c *PeerConn) runDownload() {
	for !c.t.Done() && !c.closing && (c.usInterested || c.peerInterested) {
		if c.RemoteChoking() {
			log.Debugf("will not download this tick, %s is choking", c.id.String())
			time.Sleep(time.Second)
			continue
		}
		// pending request
		p := c.numDownloading()
		if p >= c.MaxParalellRequests {
			time.Sleep(time.Second)
			continue
		}
		r := c.t.pt.nextRequestForDownload(c.bf)
		if r == nil {
			log.Debugf("no next piece to download for %s", c.id.String())
			time.Sleep(time.Second)
		} else {
			c.queueDownload(r)
		}
	}
	if c.closing {
		c.Close()
	} else {
		log.Debugf("peer %s is 'done'", c.id.String())
	}

	// done downloading
	if c.Done != nil {
		c.Done()
	}
}
