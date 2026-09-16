package xray

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// decodeFields walks a protobuf message and returns each field's number and raw
// payload, so tests can assert on the exact wire output of the hand-rolled
// marshalers without depending on Xray's generated code.
func decodeFields(t *testing.T, data []byte) map[int][][]byte {
	t.Helper()
	out := map[int][][]byte{}
	for len(data) > 0 {
		field, wire, n := consumeTag(data)
		if n == 0 {
			t.Fatalf("bad tag in %x", data)
		}
		data = data[n:]
		switch wire {
		case 0:
			v, m := binary.Uvarint(data)
			if m <= 0 {
				t.Fatalf("bad varint for field %d", field)
			}
			buf := binary.AppendUvarint(nil, v)
			out[field] = append(out[field], buf)
			data = data[m:]
		case 2:
			b, m := consumeBytes(data)
			if m == 0 {
				t.Fatalf("bad length-delimited for field %d", field)
			}
			out[field] = append(out[field], b)
			data = data[m:]
		default:
			t.Fatalf("unexpected wire type %d for field %d", wire, field)
		}
	}
	return out
}

func onlyField(t *testing.T, fields map[int][][]byte, num int) []byte {
	t.Helper()
	vals, ok := fields[num]
	if !ok {
		t.Fatalf("field %d missing, have %v", num, keysOf(fields))
	}
	if len(vals) != 1 {
		t.Fatalf("field %d appeared %d times, want 1", num, len(vals))
	}
	return vals[0]
}

func keysOf(m map[int][][]byte) []int {
	ks := make([]int, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestMarshalVLESSAccountFieldNumbers(t *testing.T) {
	got := marshalVLESSAccount("uuid-1", "xtls-rprx-vision")
	fields := decodeFields(t, got)

	if s := string(onlyField(t, fields, 1)); s != "uuid-1" {
		t.Errorf("account id (field 1) = %q, want %q", s, "uuid-1")
	}
	if s := string(onlyField(t, fields, 2)); s != "xtls-rprx-vision" {
		t.Errorf("account flow (field 2) = %q, want %q", s, "xtls-rprx-vision")
	}
	if _, ok := fields[3]; ok {
		t.Error("account encryption (field 3) should not be emitted")
	}
}

func TestMarshalVLESSAccountOmitsEmptyFlow(t *testing.T) {
	fields := decodeFields(t, marshalVLESSAccount("uuid-1", ""))

	if s := string(onlyField(t, fields, 1)); s != "uuid-1" {
		t.Errorf("account id = %q, want %q", s, "uuid-1")
	}
	if _, ok := fields[2]; ok {
		t.Error("empty flow should be omitted from the wire output")
	}
}

func TestMarshalUserFieldNumbers(t *testing.T) {
	account := marshalVLESSAccount("uuid-1", "xtls-rprx-vision")
	accountMsg := marshalTypedMessage(typeVLESSAccount, account)

	fields := decodeFields(t, marshalUser(2, "uuid-1@proxima", accountMsg))

	level, n := binary.Uvarint(onlyField(t, fields, 1))
	if n <= 0 || level != 2 {
		t.Errorf("user level (field 1) = %d, want 2", level)
	}
	if s := string(onlyField(t, fields, 2)); s != "uuid-1@proxima" {
		t.Errorf("user email (field 2) = %q, want %q", s, "uuid-1@proxima")
	}
	if got := onlyField(t, fields, 3); !bytes.Equal(got, accountMsg) {
		t.Error("user account (field 3) does not round-trip the TypedMessage")
	}
}

// Level 0 is the default the API server assigns every client, so it must
// survive the proto3 default-value omission and still be decodable by Xray.
func TestMarshalUserOmitsZeroLevel(t *testing.T) {
	fields := decodeFields(t, marshalUser(0, "uuid-1@proxima", []byte{0x01}))

	if _, ok := fields[1]; ok {
		t.Error("zero level should be omitted (proto3 default)")
	}
	if s := string(onlyField(t, fields, 2)); s != "uuid-1@proxima" {
		t.Errorf("user email = %q, want %q", s, "uuid-1@proxima")
	}
}

func TestMarshalTypedMessageFieldNumbers(t *testing.T) {
	fields := decodeFields(t, marshalTypedMessage(typeVLESSAccount, []byte{0xde, 0xad}))

	if s := string(onlyField(t, fields, 1)); s != "xray.proxy.vless.Account" {
		t.Errorf("typed message type (field 1) = %q", s)
	}
	if got := onlyField(t, fields, 2); !bytes.Equal(got, []byte{0xde, 0xad}) {
		t.Errorf("typed message value (field 2) = %x", got)
	}
}

// Xray resolves TypedMessage.type through serial.GetMessageType, which returns
// the bare fully-qualified proto name. A "type.googleapis.com/" prefix here
// would make every AlterInbound call fail to deserialize server-side.
func TestTypeNamesHaveNoURLPrefix(t *testing.T) {
	for _, name := range []string{typeAddUserOperation, typeRemoveUserOperation, typeVLESSAccount} {
		if bytes.Contains([]byte(name), []byte("type.googleapis.com")) {
			t.Errorf("type name %q must not carry a URL prefix", name)
		}
		if name == "" || name[0] == '/' {
			t.Errorf("type name %q must be a bare proto full name", name)
		}
	}
}

func TestMarshalAddUserOperationNesting(t *testing.T) {
	user := marshalUser(0, "uuid-1@proxima", marshalTypedMessage(typeVLESSAccount, marshalVLESSAccount("uuid-1", "xtls-rprx-vision")))

	fields := decodeFields(t, marshalAddUserOperation(user))
	if got := onlyField(t, fields, 1); !bytes.Equal(got, user) {
		t.Error("AddUserOperation.user (field 1) does not round-trip")
	}
}

func TestMarshalRemoveUserOperationUsesEmail(t *testing.T) {
	fields := decodeFields(t, marshalRemoveUserOperation("uuid-1@proxima"))

	if s := string(onlyField(t, fields, 1)); s != "uuid-1@proxima" {
		t.Errorf("RemoveUserOperation.email (field 1) = %q", s)
	}
}

func TestMarshalAlterInboundRequestFieldNumbers(t *testing.T) {
	op := marshalTypedMessage(typeRemoveUserOperation, marshalRemoveUserOperation("uuid-1@proxima"))

	fields := decodeFields(t, marshalAlterInboundRequest(&alterInboundRequest{
		Tag:       "vless-reality",
		Operation: op,
	}))

	if s := string(onlyField(t, fields, 1)); s != "vless-reality" {
		t.Errorf("AlterInboundRequest.tag (field 1) = %q", s)
	}
	if got := onlyField(t, fields, 2); !bytes.Equal(got, op) {
		t.Error("AlterInboundRequest.operation (field 2) does not round-trip")
	}
}

// End-to-end check that a full add-user request decodes back to the values a
// caller supplied, through all four levels of nesting.
func TestAddUserRequestRoundTrip(t *testing.T) {
	account := marshalVLESSAccount("11111111-2222-3333-4444-555555555555", "xtls-rprx-vision")
	user := marshalUser(0, "dev@proxima", marshalTypedMessage(typeVLESSAccount, account))
	op := marshalAddUserOperation(user)
	req := marshalAlterInboundRequest(&alterInboundRequest{
		Tag:       "vless-reality",
		Operation: marshalTypedMessage(typeAddUserOperation, op),
	})

	top := decodeFields(t, req)
	if s := string(onlyField(t, top, 1)); s != "vless-reality" {
		t.Fatalf("tag = %q", s)
	}

	opMsg := decodeFields(t, onlyField(t, top, 2))
	if s := string(onlyField(t, opMsg, 1)); s != typeAddUserOperation {
		t.Fatalf("operation type = %q", s)
	}

	addOp := decodeFields(t, onlyField(t, opMsg, 2))
	userMsg := decodeFields(t, onlyField(t, addOp, 1))
	if s := string(onlyField(t, userMsg, 2)); s != "dev@proxima" {
		t.Fatalf("email = %q", s)
	}

	accMsg := decodeFields(t, onlyField(t, userMsg, 3))
	if s := string(onlyField(t, accMsg, 1)); s != typeVLESSAccount {
		t.Fatalf("account type = %q", s)
	}

	acc := decodeFields(t, onlyField(t, accMsg, 2))
	if s := string(onlyField(t, acc, 1)); s != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("account id = %q", s)
	}
	if s := string(onlyField(t, acc, 2)); s != "xtls-rprx-vision" {
		t.Fatalf("account flow = %q", s)
	}
}

func TestStatsCodecMarshalsAlterInbound(t *testing.T) {
	c := statsCodec{}

	got, err := c.Marshal(&alterInboundRequest{Tag: "t", Operation: []byte{0x01}})
	if err != nil {
		t.Fatalf("Marshal(alterInboundRequest) error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Marshal(alterInboundRequest) produced no bytes")
	}

	if err := c.Unmarshal(nil, &alterInboundResponse{}); err != nil {
		t.Errorf("Unmarshal into empty AlterInboundResponse: %v", err)
	}
}

func TestStatsCodecStillMarshalsQueryStats(t *testing.T) {
	got, err := statsCodec{}.Marshal(&queryStatsRequest{Pattern: "user>>>", Reset_: true})
	if err != nil {
		t.Fatalf("Marshal(queryStatsRequest) error: %v", err)
	}

	fields := decodeFields(t, got)
	if s := string(onlyField(t, fields, 1)); s != "user>>>" {
		t.Errorf("pattern = %q", s)
	}
	reset, n := binary.Uvarint(onlyField(t, fields, 2))
	if n <= 0 || reset != 1 {
		t.Errorf("reset = %d, want 1", reset)
	}
}

func TestAlterInboundRejectsEmptyArguments(t *testing.T) {
	c := &StatsClient{}

	if err := c.AddVLESSUser(t.Context(), "", VLESSUser{UUID: "u", Email: "e"}); err == nil {
		t.Error("AddVLESSUser with empty tag should fail")
	}
	if err := c.AddVLESSUser(t.Context(), "tag", VLESSUser{Email: "e"}); err == nil {
		t.Error("AddVLESSUser with empty uuid should fail")
	}
	if err := c.AddVLESSUser(t.Context(), "tag", VLESSUser{UUID: "u"}); err == nil {
		t.Error("AddVLESSUser with empty email should fail")
	}
	if err := c.RemoveVLESSUser(t.Context(), "", "e"); err == nil {
		t.Error("RemoveVLESSUser with empty tag should fail")
	}
	if err := c.RemoveVLESSUser(t.Context(), "tag", ""); err == nil {
		t.Error("RemoveVLESSUser with empty email should fail")
	}
}
