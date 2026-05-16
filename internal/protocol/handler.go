package protocol

// Handler defines the interface for handling incoming protocol messages.
// Implemented by the networking layer (peer.Swarm) in phase 2.
type Handler interface {
	HandleHandshake(msg *HandshakeMsg)
	HandleManifest(msg *ManifestMsg)
	HandleBitfield(msg *BitfieldMsg)
	HandleHave(msg *HaveMsg)
	HandleRequest(msg *RequestMsg)
	HandlePiece(msg *PieceMsg)
	HandleCancel(msg *CancelMsg)
	HandleSync(msg *SyncMsg)
	HandlePeerList(msg *PeerListMsg)
	HandleKeepalive(msg *KeepaliveMsg)
}

// Dispatch routes a decoded Message to the appropriate Handler method.
func Dispatch(h Handler, msg Message) {
	switch m := msg.(type) {
	case *HandshakeMsg:
		h.HandleHandshake(m)
	case *ManifestMsg:
		h.HandleManifest(m)
	case *BitfieldMsg:
		h.HandleBitfield(m)
	case *HaveMsg:
		h.HandleHave(m)
	case *RequestMsg:
		h.HandleRequest(m)
	case *PieceMsg:
		h.HandlePiece(m)
	case *CancelMsg:
		h.HandleCancel(m)
	case *SyncMsg:
		h.HandleSync(m)
	case *PeerListMsg:
		h.HandlePeerList(m)
	case *KeepaliveMsg:
		h.HandleKeepalive(m)
	}
}
