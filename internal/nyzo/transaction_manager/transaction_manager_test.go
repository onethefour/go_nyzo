package transaction_manager

import (
	"github.com/onethefour/go_nyzo/internal/nyzo/block_authority"
	"github.com/onethefour/go_nyzo/internal/nyzo/block_file_handler"
	"github.com/onethefour/go_nyzo/internal/nyzo/blockchain_data"
	"github.com/onethefour/go_nyzo/internal/nyzo/configuration"
	"github.com/onethefour/go_nyzo/internal/nyzo/cycle_authority"
	"github.com/onethefour/go_nyzo/internal/nyzo/interfaces"
	"github.com/onethefour/go_nyzo/internal/nyzo/key_value_store"
	"github.com/onethefour/go_nyzo/pkg/identity"
	"os"
	"testing"
)

// Test node persisting/loading
func TestTransactionManager(t *testing.T) {
	configuration.DataDirectory = "../../../test/test_data"
	_ = os.MkdirAll(configuration.DataDirectory+"/"+configuration.SeedTransactionDirectory, os.ModePerm)
	transactionManager := &state{}
	transactionManager.ctxt = &interfaces.Context{}
	transactionManager.ctxt.BlockAuthority = block_authority.NewBlockAuthority(transactionManager.ctxt)
	transactionManager.ctxt.PersistentData = key_value_store.NewKeyValueStore(configuration.DataDirectory+"/"+configuration.PersistentDataFileName, transactionManager.ctxt.WaitGroup)
	transactionManager.ctxt.BlockFileHandler = block_file_handler.NewBlockFileHandler(transactionManager.ctxt)
	transactionManager.ctxt.CycleAuthority = cycle_authority.NewCycleAuthority(transactionManager.ctxt)
	transactionManager.ctxt.Preferences = key_value_store.NewKeyValueStore(configuration.DataDirectory+"/"+configuration.PreferencesFileName, transactionManager.ctxt.WaitGroup)
	_ = transactionManager.ctxt.BlockAuthority.Initialize()
	transactionManager.seedTransactionCache = make(map[int64]*blockchain_data.Transaction)
	transactionManager.frozenEdgeHeight = 6659310
	transactionManager.cacheSeedTransactions()
	transaction := transactionManager.SeedTransactionForBlock(6659314)
	if identity.BytesToNyzoHex(transaction.Signature) != "8f90cf2f9d6862cd-5a0deb87fe965070-679dbc98c1f791a9-de3ce54f163215db-ad04f48f296283eb-1600c74c668008f8-6e194b2b68291708-1f6eb7a8fefcd50c" {
		t.Error("Seed transaction signature does not match.")
	}
}
