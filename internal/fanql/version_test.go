package fanql

import "testing"

func TestMissingVersionNotice(t *testing.T) {
	notice := MissingVersionNotice()
	if notice.Path != "fanql_version" || notice.Message != "fanql_version missing; assuming 1" || notice.Span != nil {
		t.Fatalf("notice: %+v", notice)
	}
	got, failure := (VersionRequest{}).Validate()
	if failure != nil || got == nil || *got != notice {
		t.Fatalf("omission: %+v %+v", got, failure)
	}
}
