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
	// Note: the historical PaymentsgetServerInfo version gate was removed when
	// we bumped the SDK to v3.7.2 (ServerInfo field was renamed). The
	// minimum-version assertion is also no longer needed for the legacy /policies
	// path. NB: the legacy GetPoolBalances PIT path is known empty under
	// payments v3 — see ledger#1416 sibling and the V1 SDKPaymentsResolver
	// which uses V3GetPoolBalancesLatest instead.
	balances, err := s.client.GetPoolBalances(
		ctx,
		operations.GetPoolBalancesRequest{
			At:     at,
			PoolID: paymentPoolID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool balances: %w", err)
	}

	if balances.StatusCode != 200 {
		return nil, errors.New("failed to get pool balances")
	}

	if balances.PoolBalancesResponse == nil {
		return nil, errors.New("no pool balance")
	}

	balanceMap := make(map[string]*big.Int)
	for _, balance := range balances.PoolBalancesResponse.Data.Balances {
		balanceMap[balance.GetAsset()] = balance.GetAmount()
	}

	return balanceMap, nil
}
