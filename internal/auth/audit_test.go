package auth

import "testing"

func TestAuditRejectsUnclassifiedValues(t *testing.T) {
	sqlite := newTestSQLite(t)
	audit := NewAuditStore(sqlite.DB)
	cases := []AuditEvent{
		{EventType: "login.suceeded", Outcome: "success"},
		{EventType: "login.succeeded", Outcome: "ok"},
		{EventType: "login.succeeded", Outcome: "success", TargetType: "account"},
	}
	for _, event := range cases {
		if err := audit.Record(t.Context(), event); err == nil {
			t.Fatalf("invalid audit event was written: %+v", event)
		}
	}
	var count int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("invalid audit rows = %d, want 0", count)
	}
}
