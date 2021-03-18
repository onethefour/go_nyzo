/*
Handle pre-signed seed transactions bouncing through the Nyzo chain.
*/
package transaction_manager

import (
	"fmt"
	"github.com/onethefour/go_nyzo/internal/nyzo/blockchain_data"
	"github.com/onethefour/go_nyzo/internal/nyzo/configuration"
	"github.com/onethefour/go_nyzo/internal/nyzo/messages/message_content/message_fields"
	"github.com/onethefour/go_nyzo/internal/nyzo/utilities"
	"os"
)

const (
	blocksPerFile                int64 = 10000
	transactionsPerYear          int64 = (60*60*24*365*1000 + configuration.BlockDuration - 1) / configuration.BlockDuration // round up
	totalSeedTransactions              = transactionsPerYear * 6
	lowestSeedTransactionHeight  int64 = 2
	highestSeedTransactionHeight       = lowestSeedTransactionHeight + totalSeedTransactions - 1
)

// This should be called every 30 seconds to make sure that we always have enough seed transactions to hand out.
func (s *state) cacheSeedTransactions() {
	// we need seed transactions for at least 20 more blocks (140 seconds)
	requiredHeight := s.frozenEdgeHeight + 20
	if requiredHeight > highestSeedTransactionHeight {
		requiredHeight = highestSeedTransactionHeight
	}
	if s.highestCachedSeedTransaction < requiredHeight {
		currentFileIndex := s.frozenEdgeHeight / blocksPerFile
		// cache this file and the next
		for i := currentFileIndex; i < currentFileIndex+2; i++ {
			fileName := fmt.Sprintf(configuration.DataDirectory+"/"+configuration.SeedTransactionDirectory+"/%06d.nyzotransaction", i)
			onlineName := fmt.Sprintf(configuration.SeedTransactionSource+"/%06d.nyzotransaction", i)
			if utilities.FileDoesNotExists(fileName) {
				_ = utilities.DownloadFile(onlineName, fileName)
			}
			s.cacheTransactionsFromFile(fileName)
		}
		// delete previous file if it exists
		previousFile := fmt.Sprintf(configuration.DataDirectory+"/"+configuration.SeedTransactionDirectory+"/%06d.nyzotransaction", currentFileIndex-1)
		_ = os.Remove(previousFile)
	}
	// remove old transactions from cache
	s.seedTransactionCacheLock.Lock()
	for height := range s.seedTransactionCache {
		if height < s.frozenEdgeHeight {
			delete(s.seedTransactionCache, height)
		}
	}
	s.seedTransactionCacheLock.Unlock()
}

// Load transactions in the given file into the cache.
func (s *state) cacheTransactionsFromFile(fileName string) {
	successful := false
	f, err := os.Open(fileName)
	if err == nil {
		defer f.Close()
		transactionsRead := 0
		transactionCount, err := message_fields.ReadInt32(f)
		if err != nil {
			transactionCount = 0
		}
		for i := 0; i < int(transactionCount); i++ {
			height, err := message_fields.ReadInt64(f)
			if err != nil {
				break
			}
			transaction, err := blockchain_data.ReadNewTransaction(f)
			if err != nil {
				break
			}
			transaction.PreviousBlockHash = s.ctxt.BlockAuthority.GetGenesisBlockHash()
			if height > 0 && transaction.SignatureIsValid() {
				s.seedTransactionCacheLock.Lock()
				s.seedTransactionCache[height] = transaction
				s.seedTransactionCacheLock.Unlock()
				if height > s.highestCachedSeedTransaction {
					s.highestCachedSeedTransaction = height
				}
				transactionsRead++
			}
		}
		if transactionsRead == int(transactionCount) {
			successful = true
		}
	}
	// remove the file (for later re-download) if we had an issue reading it
	if !successful {
		_ = os.Remove(fileName)
	}
}

// Hands out a seed transaction for the given block. Only works on and after the frozen edge.
func (s *state) SeedTransactionForBlock(height int64) *blockchain_data.Transaction {
	s.seedTransactionCacheLock.Lock()
	transaction, _ := s.seedTransactionCache[height]
	s.seedTransactionCacheLock.Unlock()
	return transaction
}
