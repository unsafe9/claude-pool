package pool

import (
	"strings"
	"testing"
	"time"
)

const sampleBlob = `{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc","refreshToken":"sk-ant-ort-xyz","expiresAt":1780000000000,"refreshTokenExpiresAt":1782000000000,"scopes":["user:inference","user:profile"],"subscriptionType":"max"}}`

func TestParseBlob(t *testing.T) {
	od, err := ParseBlob(sampleBlob)
	if err != nil {
		t.Fatalf("ParseBlob: %v", err)
	}
	if od.AccessToken != "sk-ant-oat-abc" {
		t.Errorf("AccessToken = %q", od.AccessToken)
	}
	if od.RefreshToken != "sk-ant-ort-xyz" {
		t.Errorf("RefreshToken = %q", od.RefreshToken)
	}
	if od.ExpiresAt != 1780000000000 {
		t.Errorf("ExpiresAt = %d", od.ExpiresAt)
	}
	// The grant's own deadline: what tells an idle account apart from a dead one.
	if od.RefreshTokenExpiresAt != 1782000000000 {
		t.Errorf("RefreshTokenExpiresAt = %d", od.RefreshTokenExpiresAt)
	}
	if od.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q", od.SubscriptionType)
	}
}

// The top plan was renamed from Max to Ultra; credentials from either era load.
func TestParseBlob_SubscriptionTypes(t *testing.T) {
	for _, typ := range []string{"max", "ultra"} {
		blob := strings.Replace(sampleBlob, `"subscriptionType":"max"`, `"subscriptionType":"`+typ+`"`, 1)
		od, err := ParseBlob(blob)
		if err != nil {
			t.Fatalf("ParseBlob(%s): %v", typ, err)
		}
		if od.SubscriptionType != typ {
			t.Errorf("SubscriptionType = %q, want %q", od.SubscriptionType, typ)
		}
	}
}

func TestParseBlob_Invalid(t *testing.T) {
	if _, err := ParseBlob("not json"); err == nil {
		t.Error("expected error for invalid JSON")
	}
	// Any JSON object unmarshals fine — the accessToken requirement is what
	// rejects logged-out stubs and foreign payloads.
	if _, err := ParseBlob(`{"foo":1}`); err == nil {
		t.Error("expected error for JSON without claudeAiOauth")
	}
	if _, err := ParseBlob(`{"claudeAiOauth":{"refreshToken":"x"}}`); err == nil {
		t.Error("expected error for blob missing accessToken")
	}
}

func TestIsExpired(t *testing.T) {
	now := time.Now().UnixMilli()
	if !IsExpired(now - 1000) {
		t.Error("past timestamp should be expired")
	}
	if !IsExpired(now + 60_000) {
		t.Error("token expiring within the 5m buffer should count as expired")
	}
	if IsExpired(now + 30*60*1000) {
		t.Error("token 30m out should not be expired")
	}
	if IsExpired(0) {
		t.Error("zero (unknown) expiry should not be treated as expired")
	}
}
