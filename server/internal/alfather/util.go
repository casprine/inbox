package alfather

import (
	"bytes"
	"encoding/hex"
	"io"
)

func hexBytes(s string) ([]byte, error) { return hex.DecodeString(s) }

func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

func trimSpace(b []byte) []byte { return bytes.TrimSpace(b) }
