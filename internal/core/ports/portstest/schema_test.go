package portstest

import (
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestFakeSchemaRegistryContract(t *testing.T) {
	RunSchemaDecoderContract(t, func(*testing.T) ports.SchemaDecoder { return NewContractSchemaRegistry() })
}
