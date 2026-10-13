package nodeprov

// RelayFlowReport is a complete snapshot of the Entry's recently observed
// TCP flow tuples. Known distinguishes a successful empty observation from a
// missing observer. Consumers must also require fresh server receipt time.
type RelayFlowReport struct {
	Known bool        `json:"known"`
	Flows []RelayFlow `json:"flows"`
}

const MaxRelayFlowReport = 262144
