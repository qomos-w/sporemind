package domain

// Protocol tests for the coordinator.guidance namespace (user capability
// profile + guidance records). These validate that the newly defined wire
// shapes are registered in the static schema fragment and survive the binary +
// JSON wire formats exactly as the runtime encodes them.

import (
	"encoding/json"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// guidanceEntry builds a populated capability entry for round-trip checks.
func guidanceEntry() gen.GuidanceCapabilityEntry {
	return gen.GuidanceCapabilityEntry{
		Capability:  "go-testing",
		Familiarity: 4,
		Confidence:  0.85,
		Evidence:    []string{"wrote table-driven tests", "runs go test ./..."},
		UpdatedAt:   "2026-08-11T10:00:00Z",
	}
}

func guidanceProfile() gen.GuidanceCapabilityProfile {
	return gen.GuidanceCapabilityProfile{
		Account:      "acct-1",
		Capabilities: []gen.GuidanceCapabilityEntry{guidanceEntry()},
		UpdatedAt:    "2026-08-11T10:00:00Z",
	}
}

func guidanceRecord() gen.GuidanceRecord {
	return gen.GuidanceRecord{
		ID:        "rec-1",
		Topic:     "style",
		Account:   "acct-1",
		Detail:    "prefer concise commit messages",
		CreatedAt: "2026-08-11T09:00:00Z",
		UpdatedAt: "2026-08-11T09:30:00Z",
	}
}

// TestProtocol_GuidanceCapabilityEntryRoundTrip verifies the entry survives
// binary encode/decode with all fields intact, including the optional Evidence
// slice.
func TestProtocol_GuidanceCapabilityEntryRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	entry := guidanceEntry()
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceCapabilityEntry"), entry).(gen.GuidanceCapabilityEntry)
	protocolMustEqual(t, "Capability", decoded.Capability, entry.Capability)
	protocolMustEqual(t, "Familiarity", decoded.Familiarity, entry.Familiarity)
	protocolMustEqual(t, "Confidence", decoded.Confidence, entry.Confidence)
	protocolMustEqual(t, "Evidence", decoded.Evidence, entry.Evidence)
	protocolMustEqual(t, "UpdatedAt", decoded.UpdatedAt, entry.UpdatedAt)
}

// TestProtocol_GuidanceProfileRoundTrip verifies the profile and its nested
// entries round-trip over the wire.
func TestProtocol_GuidanceProfileRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	prof := guidanceProfile()
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceCapabilityProfile"), prof).(gen.GuidanceCapabilityProfile)
	protocolMustEqual(t, "Account", decoded.Account, prof.Account)
	protocolMustEqual(t, "UpdatedAt", decoded.UpdatedAt, prof.UpdatedAt)
	protocolMustEqual(t, "Capabilities", decoded.Capabilities, prof.Capabilities)
}

// TestProtocol_GuidanceRecordRoundTrip verifies the guidance record shape.
func TestProtocol_GuidanceRecordRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	rec := guidanceRecord()
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceRecord"), rec).(gen.GuidanceRecord)
	protocolMustEqual(t, "ID", decoded.ID, rec.ID)
	protocolMustEqual(t, "Topic", decoded.Topic, rec.Topic)
	protocolMustEqual(t, "Detail", decoded.Detail, rec.Detail)
	protocolMustEqual(t, "Account", decoded.Account, rec.Account)
}

// TestProtocol_GuidanceProfileUpdateRoundTrip verifies the update request
// carries the full capability set and optional records.
func TestProtocol_GuidanceProfileUpdateRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	req := gen.GuidanceProfileUpdateReq{
		Account:      "acct-1",
		Capabilities: []gen.GuidanceCapabilityEntry{guidanceEntry()},
		Records:      []gen.GuidanceRecord{guidanceRecord()},
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileUpdateReq"), req).(gen.GuidanceProfileUpdateReq)
	protocolMustEqual(t, "Account", decoded.Account, req.Account)
	protocolMustEqual(t, "Capabilities", decoded.Capabilities, req.Capabilities)
	protocolMustEqual(t, "Records", decoded.Records, req.Records)

	resp := gen.GuidanceProfileUpdateResp{Profile: guidanceProfile()}
	decodedResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileUpdateResp"), resp).(gen.GuidanceProfileUpdateResp)
	protocolMustEqual(t, "Profile", decodedResp.Profile, resp.Profile)
}

// TestProtocol_GuidanceProfileQueryRoundTrip verifies the query response with an
// optional profile and records round-trips.
func TestProtocol_GuidanceProfileQueryRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	prof := guidanceProfile()
	resp := gen.GuidanceProfileQueryResp{
		Profile: &prof,
		Records: []gen.GuidanceRecord{guidanceRecord()},
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileQueryResp"), resp).(gen.GuidanceProfileQueryResp)
	protocolMustEqual(t, "Profile", decoded.Profile, resp.Profile)
	protocolMustEqual(t, "Records", decoded.Records, resp.Records)

	// Empty profile (account with no recorded data) must also survive.
	empty := gen.GuidanceProfileQueryResp{}
	decodedEmpty := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileQueryResp"), empty).(gen.GuidanceProfileQueryResp)
	if decodedEmpty.Profile != nil {
		t.Errorf("empty query Profile = %#v, want nil", decodedEmpty.Profile)
	}
}

// TestProtocol_GuidanceProfileClearRoundTrip verifies the clear request with an
// optional single-capability target and the boolean outcome.
func TestProtocol_GuidanceProfileClearRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	req := gen.GuidanceProfileClearReq{Account: "acct-1", Capability: "go-testing"}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileClearReq"), req).(gen.GuidanceProfileClearReq)
	protocolMustEqual(t, "Account", decoded.Account, req.Account)
	protocolMustEqual(t, "Capability", decoded.Capability, req.Capability)

	resp := gen.GuidanceProfileClearResp{Account: "acct-1", Cleared: true}
	decodedResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "GuidanceProfileClearResp"), resp).(gen.GuidanceProfileClearResp)
	protocolMustEqual(t, "Account", decodedResp.Account, resp.Account)
	protocolMustEqual(t, "Cleared", decodedResp.Cleared, resp.Cleared)
}

// TestProtocol_GuidanceProfileQueryReqOmitOptional verifies that the optional
// fields on the query response are omitted from JSON when empty, matching the
// optional wire semantics.
func TestProtocol_GuidanceProfileQueryRespOmitOptional(t *testing.T) {
	empty := gen.GuidanceProfileQueryResp{}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Profile", "Records"} {
		if _, ok := m[k]; ok {
			t.Errorf("GuidanceProfileQueryResp JSON should omit empty field %q (raw: %s)", k, data)
		}
	}
}
