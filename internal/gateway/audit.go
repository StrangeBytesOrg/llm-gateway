package gateway

import "encoding/json"

type LedgerEntry struct {
	ID        int64  `json:"id"`
	AmountUSD string `json:"amount_usd"`
	Source    string `json:"source"`
	Reason    string `json:"reason"`
	Created   int64  `json:"created_unix"`
}

type AuditEntry struct {
	ID      int64           `json:"id"`
	Action  string          `json:"action"`
	Target  string          `json:"target"`
	Detail  json.RawMessage `json:"detail"`
	Created int64           `json:"created_unix"`
}
