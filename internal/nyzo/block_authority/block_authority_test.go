package block_authority

import (
	"go_nyzo/internal/nyzo/block_file_handler"
	"go_nyzo/internal/nyzo/configuration"
	"go_nyzo/internal/nyzo/cycle_authority"
	"go_nyzo/internal/nyzo/interfaces"
	"go_nyzo/internal/nyzo/transaction_manager"
	"testing"
)

var ctxt interfaces.Context

func TestVerifyIndividualBlock(t *testing.T) {
	block := ctxt.BlockFileHandler.GetBlock(5451011)
	if !ctxt.BlockAuthority.BlockIsValid(block) {
		t.Error("Block could not be verified.")
	}
}

func init() {
	ctxt = interfaces.Context{}
	configuration.DataDirectory = "../../../test/test_data"
	ctxt.BlockFileHandler = block_file_handler.NewBlockFileHandler(&ctxt)
	ctxt.BlockAuthority = NewBlockAuthority(&ctxt)
	ctxt.TransactionManager = transaction_manager.NewTransactionManager(&ctxt)
	ctxt.CycleAuthority = cycle_authority.NewCycleAuthority(&ctxt)
}
