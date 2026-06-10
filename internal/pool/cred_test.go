package pool

import (
	"testing"
	"time"
)

const sampleBlob = `{"claudeAiOauth":{"accessToken":"sk-ant-oat-abc","refreshToken":"sk-ant-ort-xyz","expiresAt":1780000000000,"scopes":["user:inference","user:profile"],"subscriptionType":"max"}}`

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
	if od.SubscriptionType != "max" {
		t.Errorf("SubscriptionType = %q", od.SubscriptionType)
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
