// Copyright 2026 The kafkito Authors.
// Licensed under the Apache License, Version 2.0.

package server

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kbin"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// fakeBroker is a minimal single-node Kafka broker for handler tests. It
// speaks just enough of the protocol for a franz-go client to connect:
// Metadata reports itself as the only broker and controller, every other
// request gets an empty default response.
type fakeBroker struct {
	ln   net.Listener
	host string
	port int32

	mu    sync.Mutex
	conns []net.Conn
	wg    sync.WaitGroup
}

const fakeBrokerNodeID = 1

func startFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	port, err := strconv.ParseInt(portStr, 10, 32)
	require.NoError(t, err)
	b := &fakeBroker{ln: ln, host: host, port: int32(port)}
	b.wg.Add(1)
	go b.accept()
	t.Cleanup(b.close)
	return b
}

func (b *fakeBroker) addr() string {
	return net.JoinHostPort(b.host, strconv.Itoa(int(b.port)))
}

func (b *fakeBroker) accept() {
	defer b.wg.Done()
	for {
		conn, err := b.ln.Accept()
		if err != nil {
			return
		}
		b.mu.Lock()
		b.conns = append(b.conns, conn)
		b.mu.Unlock()
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			defer func() { _ = conn.Close() }()
			_ = b.serve(conn)
		}()
	}
}

func (b *fakeBroker) close() {
	_ = b.ln.Close()
	b.mu.Lock()
	for _, c := range b.conns {
		_ = c.Close()
	}
	b.mu.Unlock()
	b.wg.Wait()
}

func (b *fakeBroker) serve(conn net.Conn) error {
	for {
		var size int32
		if err := binary.Read(conn, binary.BigEndian, &size); err != nil {
			return err
		}
		msg := make([]byte, size)
		if _, err := io.ReadFull(conn, msg); err != nil {
			return err
		}
		if len(msg) < 8 {
			return errors.New("short request")
		}
		rd := kbin.Reader{Src: msg}
		key := rd.Int16()
		version := rd.Int16()
		corrID := rd.Int32()

		var resp kmsg.Response
		switch key {
		case kmsg.ApiVersions.Int16():
			r := kmsg.NewPtrApiVersionsResponse()
			r.SetVersion(min(version, 3))
			for _, k := range []struct{ key, max int16 }{
				{kmsg.ApiVersions.Int16(), 3},
				{kmsg.Metadata.Int16(), 12},
			} {
				ak := kmsg.NewApiVersionsResponseApiKey()
				ak.ApiKey, ak.MinVersion, ak.MaxVersion = k.key, 0, k.max
				r.ApiKeys = append(r.ApiKeys, ak)
			}
			resp = r
		case kmsg.Metadata.Int16():
			r := kmsg.NewPtrMetadataResponse()
			r.SetVersion(version)
			br := kmsg.NewMetadataResponseBroker()
			br.NodeID, br.Host, br.Port = fakeBrokerNodeID, b.host, b.port
			r.Brokers = append(r.Brokers, br)
			r.ControllerID = fakeBrokerNodeID
			clusterID := "fake"
			r.ClusterID = &clusterID
			resp = r
		default:
			// Answer anything else with an empty default response so the
			// client does not wait for a timeout.
			req := kmsg.RequestForKey(key)
			if req == nil {
				return errors.New("unknown api key")
			}
			resp = req.ResponseKind()
			resp.SetVersion(version)
		}

		out := kbin.AppendInt32(nil, corrID)
		// ApiVersions responses always use header v0; other flexible
		// responses carry an (empty) tagged-field section.
		if resp.IsFlexible() && key != kmsg.ApiVersions.Int16() {
			out = append(out, 0)
		}
		out = resp.AppendTo(out)
		frame := kbin.AppendInt32(nil, int32(len(out))) //nolint:gosec // G115: fake responses are a few hundred bytes
		if _, err := conn.Write(append(frame, out...)); err != nil {
			return err
		}
	}
}
