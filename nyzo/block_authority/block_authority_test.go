package block_authority

import (
	"github.com/onethefour/go_nyzo/nyzo/block_file_handler"
	"github.com/onethefour/go_nyzo/nyzo/configuration"
	"github.com/onethefour/go_nyzo/nyzo/cycle_authority"
	"github.com/onethefour/go_nyzo/nyzo/interfaces"
	"github.com/onethefour/go_nyzo/nyzo/transaction_manager"
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
