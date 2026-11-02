package gateway

type Overview struct {
	Users              int    `json:"users"`
	EnabledUsers       int    `json:"enabled_users"`
	ActiveKeys         int    `json:"active_keys"`
	Requests           int    `json:"requests"`
	PendingRequests    int    `json:"pending_requests"`
	UnresolvedRequests int    `json:"unresolved_requests"`
	Requests24h        int    `json:"requests_last_24h"`
	Spend24hUSD        string `json:"spend_last_24h_usd"`
}
