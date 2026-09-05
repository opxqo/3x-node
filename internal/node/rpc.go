package node

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

// These fields match Xray v26.7.28's command, user, typed_message and account protos.
// Keeping the wire adapter local avoids linking Xray's server registration graph.
type wire []byte
type wireCodec struct{}

func (wireCodec) Name() string { return "proto" }
func (wireCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.(*wire)
	if !ok {
		return nil, errors.New("invalid RPC request")
	}
	return *b, nil
}
func (wireCodec) Unmarshal(b []byte, v any) error {
	p, ok := v.(*wire)
	if !ok {
		return errors.New("invalid RPC response")
	}
	*p = append((*p)[:0], b...)
	return nil
}

func field(b []byte, n protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, n, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}
func typed(name string, b []byte) []byte { return field(field(nil, 1, []byte(name)), 2, b) }

func alterWire(tag, email string, account *Account) wire {
	op := field(nil, 1, []byte(email))
	name := "xray.app.proxyman.command.RemoveUserOperation"
	if account != nil {
		a := field(field(field(nil, 1, []byte(account.ID)), 2, []byte(account.Flow)), 3, []byte("none"))
		user := field(field(nil, 2, []byte(email)), 3, typed("xray.proxy.vless.Account", a))
		op = field(nil, 1, user)
		name = "xray.app.proxyman.command.AddUserOperation"
	}
	return field(field(nil, 1, []byte(tag)), 2, typed(name, op))
}

func invoke(ctx context.Context, conn *grpc.ClientConn, method string, request wire) (wire, error) {
	var response wire
	err := conn.Invoke(ctx, method, &request, &response, grpc.ForceCodec(wireCodec{}))
	return response, err
}

func alterUser(ctx context.Context, conn *grpc.ClientConn, tag, email string, a *Account) error {
	_, err := invoke(ctx, conn, "/xray.app.proxyman.command.HandlerService/AlterInbound", alterWire(tag, email, a))
	return err
}

func queryStats(ctx context.Context, conn *grpc.ClientConn) (map[string]int64, error) {
	b, err := invoke(ctx, conn, "/xray.app.stats.command.StatsService/QueryStats", nil)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	err = walkWire(b, func(n protowire.Number, typ protowire.Type, value []byte, number uint64) error {
		if n != 1 || typ != protowire.BytesType {
			return nil
		}
		name := ""
		var count int64
		if err := walkWire(value, func(n protowire.Number, t protowire.Type, v []byte, x uint64) error {
			if n == 1 && t == protowire.BytesType {
				name = string(v)
			}
			if n == 2 && t == protowire.VarintType {
				count = int64(x)
			}
			return nil
		}); err != nil {
			return err
		}
		if name != "" {
			out[name] = count
		}
		return nil
	})
	return out, err
}

func walkWire(b []byte, fn func(protowire.Number, protowire.Type, []byte, uint64) error) error {
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 {
			return protowire.ParseError(k)
		}
		b = b[k:]
		var v []byte
		var x uint64
		switch t {
		case protowire.BytesType:
			v, k = protowire.ConsumeBytes(b)
		case protowire.VarintType:
			x, k = protowire.ConsumeVarint(b)
		default:
			k = protowire.ConsumeFieldValue(n, t, b)
		}
		if k < 0 {
			return fmt.Errorf("invalid Xray protobuf: %w", protowire.ParseError(k))
		}
		if err := fn(n, t, v, x); err != nil {
			return err
		}
		b = b[k:]
	}
	return nil
}
