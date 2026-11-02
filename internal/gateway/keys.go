package gateway

import (
	"strings"
	"time"
)

type Key struct {
	ID      string `json:"id" doc:"Internal ID of the current credential version; changes on rotation."`
	UserID  string `json:"user_id" doc:"Immutable user ID, caller-supplied or gateway-generated."`
	Alias   string `json:"alias" doc:"Key alias, unique within its user and matched exactly in key-alias routes."`
	Status  string `json:"status" enum:"active,revoked,expired" doc:"Credential state; user enablement and budget are checked separately."`
	Prefix  string `json:"prefix"`
	Expires *int64 `json:"expires_unix"`
	Revoked bool   `json:"revoked"`
	Created int64  `json:"created_unix" doc:"Original creation time for this user's key alias, preserved across rotation."`
	Rotated *int64 `json:"rotated_unix" doc:"Most recent successful rotate operation, including create-on-rotate; null if never rotated."`
}

func keyStatus(k Key, now int64) string {
	if k.Revoked {
		return "revoked"
	}
	if k.Expires != nil && *k.Expires <= now {
		return "expired"
	}
	return "active"
}

func validKeyAlias(id string) bool {
	return strings.TrimSpace(id) != "" && len(id) <= 256
}

func newKeyCredential(user, alias string, expires *int64) (Key, string) {
	secret := ID("gw_") + ID("")
	k := Key{
		ID:      ID("key_"),
		UserID:  user,
		Alias:   alias,
		Prefix:  secret[:11],
		Expires: expires,
		Created: time.Now().Unix(),
	}
	if k.Alias == "" {
		k.Alias = k.ID
	}
	k.Status = keyStatus(k, time.Now().Unix())
	return k, secret
}
