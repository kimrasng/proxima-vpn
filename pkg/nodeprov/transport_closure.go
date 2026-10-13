package nodeprov

// TransportClosure targets exactly one previously observed authenticated TCP
// connection. AcceptedAt fences local peer reuse and Xray process generations.
// It never revokes the shared account UUID.
type TransportClosure struct {
	ID         string           `json:"id"`
	Connection TunnelConnection `json:"connection"`
}
