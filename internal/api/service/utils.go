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

func (s *Service) getPaymentPoolBalance(ctx context.Context, paymentPoolID string, _ time.Time) (map[string]*big.Int, error) {
	// Why not the V1 GetPoolBalances(at) endpoint:
	// V1.GetPoolBalances takes a PIT but returns an empty payload under
	// payments v3 — same root cause that drove the V1 SDKPaymentsResolver
	// to V3GetPoolBalancesLatest. Calling it here used to mask real drift
	// as a zero-balance result (every asset compared against 0 → "OK"),
	// which is worse than failing loudly.
	//
	// Trade-off: V3GetPoolBalancesLatest doesn't accept a PIT, so the
	// legacy /policies path now reads *current* pool balances even when
	// the caller supplied an `at` time. Acceptable because (a) the
	// previous PIT behaviour didn't actually work and (b) cross-source
	// PIT consistency for pools was never tighter than seconds anyway.
	// V1 rules express this trade-off explicitly via per-source tolerances.
	balances, err := s.client.V3GetPoolBalancesLatest(
		ctx,
		operations.V3GetPoolBalancesLatestRequest{PoolID: paymentPoolID},
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
