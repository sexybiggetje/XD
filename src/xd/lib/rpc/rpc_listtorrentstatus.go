package rpc

import (
	"encoding/json"
	"xd/lib/bittorrent/swarm"
)

type ListTorrentStatusRequest struct {
	BaseRequest
}

func (req *ListTorrentStatusRequest) ProcessRequest(sw *swarm.Swarm, w *ResponseWriter) {
	status := make(swarm.SwarmStatus)
	sw.Torrents.ForEachTorrent(func(t *swarm.Torrent) {
		status[t.MetaInfo().Infohash().Hex()] = t.GetStatus()
	})
	w.Return(status)
}

func (req *ListTorrentStatusRequest) MarshalJSON() (data []byte, err error) {
	data, err = json.Marshal(map[string]interface{}{
		ParamSwarm:  req.Swarm,
		ParamMethod: RPCListTorrentStatus,
	})
	return
}
