package eth

import (
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/golang/glog"
	"github.com/livepeer/go-livepeer/common"
)

var (
	BlocksUntilFirstClaimDeadline = big.NewInt(200)
	maxUint256                    = new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1))
)

func FormatUnits(baseAmount *big.Int, name string) string {
	if baseAmount == nil {
		return "0 " + name
	}
	amount := FromBaseUnit(baseAmount)

	if amount.Cmp(big.NewFloat(0.01)) == -1 {
		switch name {
		case "ETH":
			return fmt.Sprintf("%v WEI", baseAmount)
		default:
			return fmt.Sprintf("%v LPTU", baseAmount)
		}
	} else {
		switch name {
		case "ETH":
			return fmt.Sprintf("%v ETH", amount)
		default:
			return fmt.Sprintf("%v LPT", amount)
		}
	}
}

func ToBaseUnit(lptAmount *big.Float) *big.Int {
	decimals := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	floatDecimals := new(big.Float).SetInt(decimals)
	floatBaseAmount := new(big.Float).Mul(lptAmount, floatDecimals)

	baseAmount := new(big.Int)
	floatBaseAmount.Int(baseAmount)

	return baseAmount
}

func FromBaseUnit(baseAmount *big.Int) *big.Float {
	decimals := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	floatDecimals := new(big.Float).SetInt(decimals)
	floatBaseAmount := new(big.Float).SetInt(baseAmount)

	return new(big.Float).Quo(floatBaseAmount, floatDecimals)
}

func FormatPerc(value *big.Int) string {
	perc := ToPerc(value)

	return fmt.Sprintf("%v", perc)
}

func ToPerc(value *big.Int) float64 {
	pMultiplier := 10000.0

	return float64(value.Int64()) / pMultiplier
}

func FromPerc(perc float64) *big.Int {
	return fromPerc(perc, big.NewFloat(10000.0))
}

func FromPercOfUint256(perc float64) *big.Int {
	multiplier := new(big.Float).SetInt(new(big.Int).Div(maxUint256, big.NewInt(100)))
	return fromPerc(perc, multiplier)
}

func Wait(db *common.DB, blocks *big.Int) error {
	var (
		lastSeenBlock *big.Int
		err           error
	)

	lastSeenBlock, err = db.LastSeenBlock()
	if err != nil {
		return err
	}

	targetBlock := new(big.Int).Add(lastSeenBlock, blocks)
	tickCh := time.NewTicker(15 * time.Second).C

	glog.Infof("Waiting %v blocks...", blocks)

	for {
		select {
		case <-tickCh:
			if lastSeenBlock.Cmp(targetBlock) >= 0 {
				return nil
			}

			lastSeenBlock, err = db.LastSeenBlock()
			if err != nil {
				glog.Error("Error getting last seen block ", err)
				continue
			}
		}
	}

	return nil
}

func IsNullAddress(addr ethcommon.Address) bool {
	return addr == ethcommon.Address{}
}

func fromPerc(perc float64, multiplier *big.Float) *big.Int {
	floatRes := new(big.Float).Mul(big.NewFloat(perc), multiplier)
	intRes, _ := floatRes.Int(nil)
	return intRes
}

func parseABI(abiString string) (abi.ABI, error) {
	return abi.JSON(strings.NewReader(abiString))
}

func decodeTxParams(abi abi.ABI, v map[string]interface{}, data []byte) error {
	m, err := abi.MethodById(data[:4])
	if err != nil {
		return err
	}
	if err := m.Inputs.UnpackIntoMap(v, data[4:]); err != nil {
		return err
	}
	for k, val := range v {
		v[k] = ethTypeToStringyType(val)
	}
	return nil
}

func ethTypeToStringyType(v interface{}) interface{} {
	val := reflect.Indirect(reflect.ValueOf(v))

	switch vTy := val.Interface().(type) {
	case []byte:
		return "0x" + ethcommon.Bytes2Hex(vTy)
	case [32]byte:
		return fmt.Sprintf("0x%x", vTy)
	case ethcommon.Address:
		return vTy.Hex()
	case ethcommon.Hash:
		return "0x" + vTy.Hex()
	case big.Int:
		return vTy.String()
	default:
		return handleComplexEthType(val)
	}
}

func handleComplexEthType(val reflect.Value) interface{} {
	switch val.Kind() {
	// tuple
	case reflect.Struct:
		vString := "{"
		for i := 0; i < val.NumField(); i++ {
			vString += fmt.Sprintf(" %v", val.Type().Field(i).Name)
			vString += ": "
			vString += fmt.Sprintf("%v ", ethTypeToStringyType(val.Field(i).Interface()))
		}
		vString += "}"
		return vString
	case reflect.Array:
		return handleEthSlice(val)
	case reflect.Slice:
		return handleEthSlice(val)
	default:
		return val.Interface()
	}
}

func handleEthSlice(val reflect.Value) string {
	if val.Kind() != reflect.Array && val.Kind() != reflect.Slice {
		return ""
	}
	vString := "["
	for i := 0; i < val.Len(); i++ {
		vString += fmt.Sprintf(" %v ", ethTypeToStringyType(val.Index(i).Interface()))
	}
	vString += "]"
	return vString
}
