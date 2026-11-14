package domain

import apitypes "github.com/eyprz/visfox-srv6/api/generated/go"

// Intent is a crosspoint intent as Intent Ingestion hands it to Intent Translation (RFC 9315
// §5.1.1, §5.1.2): send the IN to the OUT through the preset's logical chain, on air or not. It is
// read from the Active Intent Table (SSoT) and the preset it references.
type Intent struct {
	// ID is the crosspoint (cell) id, e.g. IN1-OUT2.
	ID            string
	SourceID      string
	DestinationID string
	// Chain is the logical chain of the preset with its params.
	Chain []apitypes.FunctionInstance
	// OnAir: the intent is that the stream is sent (ACTIVE), not only reserved (STANDBY).
	OnAir bool
}
