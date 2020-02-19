package eth

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/golang/glog"
	"github.com/livepeer/go-livepeer/eth/contracts"
)

var abis = []string{
	contracts.BondingManagerABI,
	contracts.ControllerABI,
	contracts.LivepeerTokenABI,
	contracts.LivepeerTokenFaucetABI,
	contracts.MinterABI,
	contracts.RoundsManagerABI,
	contracts.ServiceRegistryABI,
	contracts.TicketBrokerABI,
}

type Backend interface {
	ethereum.ChainStateReader
	ethereum.TransactionReader
	ethereum.TransactionSender
	ethereum.ContractCaller
	ethereum.PendingContractCaller
	ethereum.PendingStateReader
	ethereum.GasEstimator
	ethereum.GasPricer
	ethereum.LogFilterer

	ChainID(ctx context.Context) (*big.Int, error)
}

type backend struct {
	*ethclient.Client
	methods      map[string]string
	abiMap       map[string]abi.ABI
	nonceManager *NonceManager
}

func NewBackend(client *ethclient.Client) (Backend, error) {
	methods, abiMap, err := makeMethodsMap()
	if err != nil {
		return nil, err
	}

	return &backend{
		client,
		methods,
		abiMap,
		NewNonceManager(client),
	}, nil
}

func (b *backend) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	b.nonceManager.Lock(account)
	defer b.nonceManager.Unlock(account)

	return b.nonceManager.Next(account)
}

func (b *backend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	sendErr := b.Client.SendTransaction(ctx, tx)

	// update local nonce
	msg, err := tx.AsMessage(types.HomesteadSigner{})
	if err != nil {
		return err
	}
	sender := msg.From()
	b.nonceManager.Lock(sender)
	b.nonceManager.Update(sender, tx.Nonce())
	b.nonceManager.Unlock(sender)

	data := tx.Data()
	method, ok := b.methods[string(data[:4])]
	if !ok {
		method = "unknown"
	}

	txParams := make(map[string]interface{})
	if err := decodeTxParams(b.abiMap[string(data[:4])], txParams, data); err != nil {
		return err
	}

	var txParamsString string
	for name, val := range txParams {
		txParamsString += fmt.Sprintf("%v: %v   ", name, val)
	}

	if sendErr != nil {
		glog.Infof("\n%vEth Transaction%v\n\nInvoking transaction: \"%v\".  Transaction Inputs: \"%v\"   \nTransaction Failed: %v\n\n%v\n", strings.Repeat("*", 30), strings.Repeat("*", 30), method, txParamsString, err, strings.Repeat("*", 75))
		return sendErr
	}

	glog.Infof("\n%vEth Transaction%v\n\nInvoking transaction: \"%v\". Transaction Inputs: \"%v\"  Hash: \"%v\". \n\n%v\n", strings.Repeat("*", 30), strings.Repeat("*", 30), method, txParamsString, tx.Hash().String(), strings.Repeat("*", 75))

	return nil
}

func (b *backend) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return b.retryRemoteCall(func() ([]byte, error) {
		return b.Client.CallContract(ctx, msg, blockNumber)
	})
}

func (b *backend) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	return b.retryRemoteCall(func() ([]byte, error) {
		return b.Client.PendingCallContract(ctx, msg)
	})
}

func (b *backend) retryRemoteCall(remoteCall func() ([]byte, error)) (out []byte, err error) {
	count := 3    // consider making this a package-level global constant
	retry := true // consider making this a package-level global constant

	for i := 0; i < count && retry; i++ {
		out, err = remoteCall()
		if err != nil && (err.Error() == "EOF" || err.Error() == "tls: use of closed connection") {
			glog.V(4).Infof("Retrying call to remote ethereum node")
		} else {
			retry = false
		}
	}

	return out, err
}

func makeMethodsMap() (map[string]string, map[string]abi.ABI, error) {
	methods := make(map[string]string)
	abiMap := make(map[string]abi.ABI)

	for _, ABI := range abis {
		parsedAbi, err := abi.JSON(strings.NewReader(ABI))
		if err != nil {
			return map[string]string{}, map[string]abi.ABI{}, err
		}
		for _, m := range parsedAbi.Methods {
			methods[string(m.ID())] = m.Name
			abiMap[string(m.ID())] = parsedAbi
		}
	}

	return methods, abiMap, nil
}
