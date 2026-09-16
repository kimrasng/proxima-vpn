package xray

import (
	"context"
	"encoding/binary"
	"fmt"
)

// Hand-encoded protobuf for Xray's HandlerService.AlterInbound, used to add and
// remove users on a running inbound without restarting Xray.
//
// Field numbers below are from Xray-core's .proto definitions:
//
//	app/proxyman/command/command.proto
//	  AlterInboundRequest { string tag = 1; TypedMessage operation = 2; }
//	  AddUserOperation    { User user = 1; }
//	  RemoveUserOperation { string email = 1; }
//	common/serial/typed_message.proto
//	  TypedMessage { string type = 1; bytes value = 2; }
//	common/protocol/user.proto
//	  User { uint32 level = 1; string email = 2; TypedMessage account = 3; }
//	proxy/vless/account.proto
//	  Account { string id = 1; string flow = 2; string encryption = 3; ... }
//
// TypedMessage.type carries the fully-qualified proto name with no
// "type.googleapis.com/" prefix, matching Xray's serial.GetMessageType
// (message.ProtoReflect().Descriptor().FullName()).
const (
	handlerServicePath = "/xray.app.proxyman.command.HandlerService"
	alterInboundMethod = handlerServicePath + "/AlterInbound"

	typeAddUserOperation    = "xray.app.proxyman.command.AddUserOperation"
	typeRemoveUserOperation = "xray.app.proxyman.command.RemoveUserOperation"
	typeVLESSAccount        = "xray.proxy.vless.Account"
)

// alterInboundRequest is the wire-level request for AlterInbound. Operation is
// a pre-marshaled TypedMessage.
type alterInboundRequest struct {
	Tag       string
	Operation []byte
}

// alterInboundResponse is an empty message; we only care that the RPC
// succeeded.
type alterInboundResponse struct{}

// marshalAlterInboundRequest encodes AlterInboundRequest.
func marshalAlterInboundRequest(req *alterInboundRequest) []byte {
	var buf []byte
	if req.Tag != "" {
		buf = appendTag(buf, 1, 2)
		buf = appendBytes(buf, []byte(req.Tag))
	}
	if len(req.Operation) > 0 {
		buf = appendTag(buf, 2, 2)
		buf = appendBytes(buf, req.Operation)
	}
	return buf
}

// marshalTypedMessage encodes TypedMessage{type, value}.
func marshalTypedMessage(typeName string, value []byte) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, []byte(typeName))
	buf = appendTag(buf, 2, 2)
	buf = appendBytes(buf, value)
	return buf
}

// marshalVLESSAccount encodes proxy/vless Account{id, flow}.
//
// encryption (field 3) is deliberately left unset: VLESS inbounds carry
// "decryption":"none" at the inbound level and per-account encryption is not
// used by the configs this agent serves (see the API server's
// services/xray_config.go), so emitting an empty string would only add bytes.
func marshalVLESSAccount(id, flow string) []byte {
	var buf []byte
	if id != "" {
		buf = appendTag(buf, 1, 2)
		buf = appendBytes(buf, []byte(id))
	}
	if flow != "" {
		buf = appendTag(buf, 2, 2)
		buf = appendBytes(buf, []byte(flow))
	}
	return buf
}

// marshalUser encodes common/protocol User{level, email, account}.
func marshalUser(level uint32, email string, account []byte) []byte {
	var buf []byte
	if level != 0 {
		buf = appendTag(buf, 1, 0)
		buf = binary.AppendUvarint(buf, uint64(level))
	}
	if email != "" {
		buf = appendTag(buf, 2, 2)
		buf = appendBytes(buf, []byte(email))
	}
	if len(account) > 0 {
		buf = appendTag(buf, 3, 2)
		buf = appendBytes(buf, account)
	}
	return buf
}

// marshalAddUserOperation encodes AddUserOperation{user}.
func marshalAddUserOperation(user []byte) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, user)
	return buf
}

// marshalRemoveUserOperation encodes RemoveUserOperation{email}.
func marshalRemoveUserOperation(email string) []byte {
	var buf []byte
	buf = appendTag(buf, 1, 2)
	buf = appendBytes(buf, []byte(email))
	return buf
}

// VLESSUser is a user to be provisioned on a VLESS inbound at runtime.
type VLESSUser struct {
	UUID  string
	Email string
	Flow  string
	Level uint32
}

// AddVLESSUser adds a VLESS user to the named running inbound without
// restarting Xray.
//
// Xray returns an error if the user's email is already present on that inbound,
// so callers that cannot be sure of the current state should remove first (see
// SyncVLESSUsers in the agent).
func (c *StatsClient) AddVLESSUser(ctx context.Context, inboundTag string, u VLESSUser) error {
	if inboundTag == "" {
		return fmt.Errorf("add vless user: inbound tag is required")
	}
	if u.UUID == "" || u.Email == "" {
		return fmt.Errorf("add vless user: uuid and email are required")
	}

	account := marshalVLESSAccount(u.UUID, u.Flow)
	accountMsg := marshalTypedMessage(typeVLESSAccount, account)
	user := marshalUser(u.Level, u.Email, accountMsg)
	op := marshalAddUserOperation(user)
	opMsg := marshalTypedMessage(typeAddUserOperation, op)

	req := &alterInboundRequest{Tag: inboundTag, Operation: opMsg}
	resp := &alterInboundResponse{}
	if err := c.conn.Invoke(ctx, alterInboundMethod, req, resp); err != nil {
		return fmt.Errorf("add vless user %s to %s: %w", u.Email, inboundTag, err)
	}
	return nil
}

// RemoveVLESSUser removes a user from the named running inbound by email
// without restarting Xray.
func (c *StatsClient) RemoveVLESSUser(ctx context.Context, inboundTag, email string) error {
	if inboundTag == "" {
		return fmt.Errorf("remove vless user: inbound tag is required")
	}
	if email == "" {
		return fmt.Errorf("remove vless user: email is required")
	}

	op := marshalRemoveUserOperation(email)
	opMsg := marshalTypedMessage(typeRemoveUserOperation, op)

	req := &alterInboundRequest{Tag: inboundTag, Operation: opMsg}
	resp := &alterInboundResponse{}
	if err := c.conn.Invoke(ctx, alterInboundMethod, req, resp); err != nil {
		return fmt.Errorf("remove vless user %s from %s: %w", email, inboundTag, err)
	}
	return nil
}
