package service

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/formancehq/formance-sdk-go/v3/pkg/models/operations"
)

func (s *Service) getAccountsAggregatedBalance(ctx context.Context, ledgerName string, ledgerAggregatedBalanceQuery map[string]interface{}, at time.Time) (map[string]*big.Int, error) {
	// Note: the historical V2GetInfo version gate was removed when we bumped the
	// SDK to v3.7.2 — the global ledger-info endpoint has been replaced by
	// per-ledger info and the minimum-version assertion no longer applies.
	balances, err := s.client.V2GetBalancesAggregated(
		ctx,
		operations.V2GetBalancesAggregatedRequest{
			RequestBody: ledgerAggregatedBalanceQuery,
			Ledger:      ledgerName,
			Pit:         &at,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get aggregated balances: %w", err)
	}

	if balances.StatusCode != 200 {
		return nil, errors.New("failed to get aggregated balances")
	}

	if balances.V2AggregateBalancesResponse == nil {
		return nil, errors.New("no aggregated balance")
	}

	balanceMap := make(map[string]*big.Int)
	for asset, balance := range balances.V2AggregateBalancesResponse.Data {
		balanceMap[asset] = balance
	}

	return balanceMap, nil
}

func (s *Service) getPaymentPoolBalance(ctx context.Context, paymentPoolID string, at time.Time) (map[string]*big.Int, error) {
	// Read the pool balance point-in-time at `at` (the request's
	// reconciledAtPayments, which ReconciliationRequest.Validate guarantees is a
	// non-zero past instant). Payments v3 GET /v3/pools/{id}/balances?at= returns
	// the balance valid at that instant — verified against payments v3.3.1.
	//
	// This corrects a prior workaround that read V3GetPoolBalancesLatest and
	// discarded `at`: it rested on the belief that no payments-v3 PIT read
	// existed, which is not true. The one real caveat is the balance-window tail
	// (a read strictly after the pool's last balance movement is empty on the PIT
	// route) — but the legacy contract requires `at` in the past precisely to
	// reconcile against a settled historical instant, so PIT is the faithful read
	// here. See ADR-002.
	balances, err := s.client.V3GetPoolBalances(
		ctx,
		operations.V3GetPoolBalancesRequest{PoolID: paymentPoolID, At: &at},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool balances: %w", err)
	}

	if balances.StatusCode != 200 {
		return nil, errors.New("failed to get pool balances")
	}

	if balances.V3PoolBalancesResponse == nil {
		return nil, errors.New("no pool balance")
	}

	balanceMap := make(map[string]*big.Int)
	for _, balance := range balances.V3PoolBalancesResponse.Data {
		if balance.Amount == nil {
			continue
		}
		balanceMap[balance.Asset] = balance.Amount
	}

	return balanceMap, nil
}
