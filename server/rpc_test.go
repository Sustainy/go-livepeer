package server

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/accounts"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/golang/protobuf/proto"

	"github.com/livepeer/go-livepeer/common"
	"github.com/livepeer/go-livepeer/core"
	"github.com/livepeer/go-livepeer/crypto"
	"github.com/livepeer/go-livepeer/drivers"
	"github.com/livepeer/go-livepeer/net"
	"github.com/livepeer/go-livepeer/pm"
	"github.com/livepeer/lpms/stream"
)

type mockBalance struct {
	mock.Mock
}

func (m *mockBalance) Credit(amount *big.Rat) {
	m.Called(amount)
}

func (m *mockBalance) StageUpdate(minCredit *big.Rat, ev *big.Rat) (int, *big.Rat, *big.Rat) {
	args := m.Called(minCredit, ev)
	var newCredit *big.Rat
	var existingCredit *big.Rat

	if args.Get(1) != nil {
		newCredit = args.Get(1).(*big.Rat)
	}

	if args.Get(2) != nil {
		existingCredit = args.Get(2).(*big.Rat)
	}

	return args.Int(0), newCredit, existingCredit
}

func (m *mockBalance) Clear() {
	m.Called()
}

type stubOrchestrator struct {
	priv         *ecdsa.PrivateKey
	block        *big.Int
	signErr      error
	sessCapErr   error
	ticketParams *net.TicketParams
	priceInfo    *net.PriceInfo
	serviceURI   string
}

func (r *stubOrchestrator) ServiceURI() *url.URL {
	if r.serviceURI == "" {
		r.serviceURI = "http://localhost:1234"
	}
	url, _ := url.Parse(r.serviceURI)
	return url
}

func (r *stubOrchestrator) CurrentBlock() *big.Int {
	return r.block
}

func (r *stubOrchestrator) Sign(msg []byte) ([]byte, error) {
	if r.signErr != nil {
		return nil, r.signErr
	}

	ethMsg := accounts.TextHash(ethcrypto.Keccak256(msg))
	sig, err := ethcrypto.Sign(ethMsg, r.priv)
	if err != nil {
		return nil, err
	}

	// sig is in the [R || S || V] format where V is 0 or 1
	// Convert the V param to 27 or 28
	v := sig[64]
	if v == byte(0) || v == byte(1) {
		v += 27
	}

	return append(sig[:64], v), nil
}

func (r *stubOrchestrator) VerifySig(addr ethcommon.Address, msg string, sig []byte) bool {
	return crypto.VerifySig(addr, ethcrypto.Keccak256([]byte(msg)), sig)
}

func (r *stubOrchestrator) Address() ethcommon.Address {
	return ethcrypto.PubkeyToAddress(r.priv.PublicKey)
}
func (r *stubOrchestrator) TranscodeSeg(md *core.SegTranscodingMetadata, seg *stream.HLSSegment) (*core.TranscodeResult, error) {
	return nil, nil
}
func (r *stubOrchestrator) StreamIDs(jobID string) ([]core.StreamID, error) {
	return []core.StreamID{}, nil
}

func (r *stubOrchestrator) ProcessPayment(payment net.Payment, manifestID core.ManifestID) error {
	return nil
}

func (r *stubOrchestrator) TicketParams(sender ethcommon.Address) (*net.TicketParams, error) {
	return r.ticketParams, nil
}

func (r *stubOrchestrator) PriceInfo(sender ethcommon.Address) (*net.PriceInfo, error) {
	return r.priceInfo, nil
}

func (r *stubOrchestrator) SufficientBalance(addr ethcommon.Address, manifestID core.ManifestID) bool {
	return false
}

func (r *stubOrchestrator) DebitFees(addr ethcommon.Address, manifestID core.ManifestID, price *net.PriceInfo, pixels int64) {
}

func newStubOrchestrator() *stubOrchestrator {
	pk, err := ethcrypto.GenerateKey()
	if err != nil {
		return &stubOrchestrator{}
	}
	return &stubOrchestrator{priv: pk, block: big.NewInt(5)}
}

func (r *stubOrchestrator) CheckCapacity(mid core.ManifestID) error {
	return r.sessCapErr
}
func (r *stubOrchestrator) ServeTranscoder(stream net.Transcoder_RegisterTranscoderServer, capacity int) {
}
func (r *stubOrchestrator) TranscoderResults(job int64, res *core.RemoteTranscoderResult) {
}
func (r *stubOrchestrator) TranscoderSecret() string {
	return ""
}
func stubBroadcaster2() *stubOrchestrator {
	return newStubOrchestrator() // lazy; leverage subtyping for interface commonalities
}

func TestRPCTranscoderReq(t *testing.T) {

	o := newStubOrchestrator()
	b := stubBroadcaster2()

	req, err := genOrchestratorReq(b)
	if err != nil {
		t.Error("Unable to create orchestrator req ", req)
	}

	addr := ethcommon.BytesToAddress(req.Address)
	if verifyOrchestratorReq(o, addr, req.Sig) != nil { // normal case
		t.Error("Unable to verify orchestrator request")
	}

	// wrong broadcaster
	addr = ethcrypto.PubkeyToAddress(stubBroadcaster2().priv.PublicKey)
	if verifyOrchestratorReq(o, addr, req.Sig) == nil {
		t.Error("Did not expect verification to pass; should mismatch broadcaster")
	}

	// invalid address
	addr = ethcommon.BytesToAddress([]byte("#non-hex address!"))
	if verifyOrchestratorReq(o, addr, req.Sig) == nil {
		t.Error("Did not expect verification to pass; should mismatch broadcaster")
	}
	addr = ethcommon.BytesToAddress(req.Address)

	// at capacity
	o.sessCapErr = fmt.Errorf("At capacity")
	if err := verifyOrchestratorReq(o, addr, req.Sig); err != o.sessCapErr {
		t.Errorf("Expected %v; got %v", o.sessCapErr, err)
	}
	o.sessCapErr = nil

	// error signing
	b.signErr = fmt.Errorf("Signing error")
	_, err = genOrchestratorReq(b)
	if err == nil {
		t.Error("Did not expect to generate a orchestrator request with invalid address")
	}
}

func TestRPCSeg(t *testing.T) {
	mid := core.RandomManifestID()
	b := stubBroadcaster2()
	o := newStubOrchestrator()
	s := &BroadcastSession{
		Broadcaster: b,
		ManifestID:  mid,
	}

	baddr := ethcrypto.PubkeyToAddress(b.priv.PublicKey)

	segData := &stream.HLSSegment{}

	creds, err := genSegCreds(s, segData)
	if err != nil {
		t.Error("Unable to generate seg creds ", err)
		return
	}
	if _, err := verifySegCreds(o, creds, baddr); err != nil {
		t.Error("Unable to verify seg creds", err)
		return
	}

	// error signing
	b.signErr = fmt.Errorf("SignErr")
	if _, err := genSegCreds(s, segData); err != b.signErr {
		t.Error("Generating seg creds ", err)
	}
	b.signErr = nil

	// test invalid bcast addr
	oldAddr := baddr
	key, _ := ethcrypto.GenerateKey()
	baddr = ethcrypto.PubkeyToAddress(key.PublicKey)
	if _, err := verifySegCreds(o, creds, baddr); err != errSegSig {
		t.Error("Unexpectedly verified seg creds: invalid bcast addr", err)
	}
	baddr = oldAddr

	// sanity check
	if _, err := verifySegCreds(o, creds, baddr); err != nil {
		t.Error("Sanity check failed", err)
	}

	// test corrupt creds
	idx := len(creds) / 2
	kreds := creds[:idx] + string(^creds[idx]) + creds[idx+1:]
	if _, err := verifySegCreds(o, kreds, baddr); err != errSegEncoding {
		t.Error("Unexpectedly verified bad creds", err)
	}

	corruptSegData := func(segData *net.SegData, expectedErr error) {
		data, _ := proto.Marshal(segData)
		creds = base64.StdEncoding.EncodeToString(data)
		if _, err := verifySegCreds(o, creds, baddr); err != expectedErr {
			t.Errorf("Expected to fail with '%v' but got '%v'", expectedErr, err)
		}
	}

	// corrupt profiles
	corruptSegData(&net.SegData{Profiles: []byte("abc")}, common.ErrProfile)

	// corrupt sig
	sd := &net.SegData{ManifestId: []byte(s.ManifestID)}
	corruptSegData(sd, errSegSig) // missing sig
	sd.Sig = []byte("abc")
	corruptSegData(sd, errSegSig) // invalid sig

	// at capacity
	sd = &net.SegData{ManifestId: []byte(s.ManifestID)}
	sd.Sig, _ = b.Sign((&core.SegTranscodingMetadata{ManifestID: s.ManifestID}).Flatten())
	o.sessCapErr = fmt.Errorf("At capacity")
	corruptSegData(sd, o.sessCapErr)
	o.sessCapErr = nil
}

func TestPing(t *testing.T) {
	o := newStubOrchestrator()

	tsSignature, _ := o.Sign([]byte(fmt.Sprintf("%v", time.Now())))
	pingSent := ethcrypto.Keccak256(tsSignature)
	req := &net.PingPong{Value: pingSent}

	pong, err := ping(context.Background(), req, o)
	if err != nil {
		t.Error("Unable to send Ping request")
	}

	verified := o.VerifySig(o.Address(), string(pingSent), pong.Value)

	if !verified {
		t.Error("Unable to verify response from ping request")
	}
}

func TestValidatePrice(t *testing.T) {
	assert := assert.New(t)
	mid := core.RandomManifestID()
	b := stubBroadcaster2()
	oinfo := &net.OrchestratorInfo{
		PriceInfo: &net.PriceInfo{
			PricePerUnit:  1,
			PixelsPerUnit: 3,
		},
	}

	s := &BroadcastSession{
		Broadcaster:      b,
		ManifestID:       mid,
		OrchestratorInfo: oinfo,
		PMSessionID:      "foo",
	}

	// B's MaxPrice is nil
	err := validatePrice(s)
	assert.Nil(err)

	// B MaxPrice > O Price
	BroadcastCfg.SetMaxPrice(big.NewRat(5, 1))
	err = validatePrice(s)
	assert.Nil(err)

	// B MaxPrice == O Price
	BroadcastCfg.SetMaxPrice(big.NewRat(1, 3))
	err = validatePrice(s)
	assert.Nil(err)

	// B MaxPrice < O Price
	BroadcastCfg.SetMaxPrice(big.NewRat(1, 5))
	err = validatePrice(s)
	assert.EqualError(err, fmt.Sprintf("Orchestrator price higher than the set maximum price of %v wei per %v pixels", int64(1), int64(5)))

	// O.PriceInfo is nil
	s.OrchestratorInfo.PriceInfo = nil
	err = validatePrice(s)
	assert.EqualError(err, "missing orchestrator price")

	// O.PriceInfo.PixelsPerUnit is 0
	s.OrchestratorInfo.PriceInfo = &net.PriceInfo{PricePerUnit: 1, PixelsPerUnit: 0}
	err = validatePrice(s)
	assert.EqualError(err, "pixels per unit is 0")
}

func TestGetPayment_GivenInvalidBase64_ReturnsError(t *testing.T) {
	header := "not base64"

	_, err := getPayment(header)

	assert.Contains(t, err.Error(), "base64")
}

func TestGetPayment_GivenEmptyHeader_ReturnsEmptyPayment(t *testing.T) {
	payment, err := getPayment("")

	assert := assert.New(t)
	assert.Nil(err)
	assert.Nil(payment.TicketParams)
	assert.Nil(payment.Sender)
	assert.Nil(payment.TicketSenderParams)
	assert.Nil(payment.ExpectedPrice)
}

func TestGetPayment_GivenNoTicketSenderParams_ZeroLength(t *testing.T) {
	var protoPayment net.Payment
	data, err := proto.Marshal(&protoPayment)
	require.Nil(t, err)
	header := base64.StdEncoding.EncodeToString(data)

	payment, err := getPayment(header)

	assert := assert.New(t)
	assert.Nil(err)
	assert.Zero(len(payment.TicketSenderParams), "TicketSenderParams slice not empty")
	assert.Nil(payment.TicketParams)
	assert.Nil(payment.Sender)
}

func TestGetPayment_GivenInvalidProtoData_ReturnsError(t *testing.T) {
	data := pm.RandBytes(123)
	header := base64.StdEncoding.EncodeToString(data)

	_, err := getPayment(header)

	assert.Contains(t, err.Error(), "protobuf")
}

func TestGetPayment_GivenValidHeader_ReturnsPayment(t *testing.T) {
	protoPayment := defaultPayment(t)
	data, err := proto.Marshal(protoPayment)
	require.Nil(t, err)
	header := base64.StdEncoding.EncodeToString(data)

	payment, err := getPayment(header)

	assert := assert.New(t)
	assert.Nil(err)

	assert.Equal(protoPayment.Sender, payment.Sender)
	assert.Equal(protoPayment.TicketParams.Recipient, payment.TicketParams.Recipient)
	assert.Equal(protoPayment.TicketParams.FaceValue, payment.TicketParams.FaceValue)
	assert.Equal(protoPayment.TicketParams.WinProb, payment.TicketParams.WinProb)
	assert.Equal(protoPayment.TicketParams.RecipientRandHash, payment.TicketParams.RecipientRandHash)
	assert.Equal(protoPayment.TicketParams.Seed, payment.TicketParams.Seed)
	assert.Zero(big.NewRat(1, 3).Cmp(big.NewRat(protoPayment.ExpectedPrice.PricePerUnit, protoPayment.ExpectedPrice.PixelsPerUnit)))

	for i, tsp := range payment.TicketSenderParams {
		assert.Equal(tsp.SenderNonce, protoPayment.TicketSenderParams[i].SenderNonce)
		assert.Equal(tsp.Sig, protoPayment.TicketSenderParams[i].Sig)
	}

}

func TestGetPaymentSender_GivenPaymentTicketSenderIsNil(t *testing.T) {
	protoPayment := defaultPayment(t)
	protoPayment.Sender = nil

	assert.Equal(t, ethcommon.Address{}, getPaymentSender(*protoPayment))
}

func TestGetPaymentSender_GivenPaymentTicketsIsZero(t *testing.T) {
	var protoPayment net.Payment
	assert.Equal(t, ethcommon.Address{}, getPaymentSender(protoPayment))
}

func TestGetPaymentSender_GivenValidPaymentTicket(t *testing.T) {
	protoPayment := defaultPayment(t)

	assert.Equal(t, ethcommon.BytesToAddress(protoPayment.Sender), getPaymentSender(*protoPayment))
}

func TestGetOrchestrator_GivenValidSig_ReturnsTranscoderURI(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	uri := "http://someuri.com"
	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(true)
	orch.On("ServiceURI").Return(url.Parse(uri))
	orch.On("TicketParams", mock.Anything).Return(nil, nil)
	orch.On("PriceInfo", mock.Anything).Return(nil, nil)
	oInfo, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert := assert.New(t)
	assert.Nil(err)
	assert.Equal(uri, oInfo.Transcoder)
}

func TestGetOrchestrator_GivenInvalidSig_ReturnsError(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(false)

	oInfo, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert := assert.New(t)
	assert.Contains(err.Error(), "sig")
	assert.Nil(oInfo)
}

func TestGetOrchestrator_GivenValidSig_ReturnsOrchTicketParams(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	uri := "http://someuri.com"
	expectedParams := defaultTicketParams()
	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(true)
	orch.On("ServiceURI").Return(url.Parse(uri))
	orch.On("TicketParams", mock.Anything).Return(expectedParams, nil)
	orch.On("PriceInfo", mock.Anything).Return(nil, nil)
	oInfo, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert := assert.New(t)
	assert.Nil(err)
	assert.Equal(expectedParams, oInfo.TicketParams)
}

func TestGetOrchestrator_TicketParamsError(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	uri := "http://someuri.com"
	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(true)
	orch.On("ServiceURI").Return(url.Parse(uri))
	expErr := errors.New("TicketParams error")
	orch.On("TicketParams", mock.Anything).Return(nil, expErr)

	_, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert := assert.New(t)
	assert.EqualError(err, expErr.Error())
}

func TestGetOrchestrator_GivenValidSig_ReturnsOrchPriceInfo(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	uri := "http://someuri.com"
	expectedPrice := &net.PriceInfo{
		PricePerUnit:  2,
		PixelsPerUnit: 3,
	}
	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(true)
	orch.On("ServiceURI").Return(url.Parse(uri))
	orch.On("TicketParams", mock.Anything).Return(nil, nil)
	orch.On("PriceInfo", mock.Anything).Return(expectedPrice, nil)
	oInfo, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert := assert.New(t)
	assert.Nil(err)
	assert.Equal(expectedPrice, oInfo.PriceInfo)
}

func TestGetOrchestrator_PriceInfoError(t *testing.T) {
	orch := &mockOrchestrator{}
	drivers.NodeStorage = drivers.NewMemoryDriver(nil)
	uri := "http://someuri.com"
	expErr := errors.New("PriceInfo error")

	orch.On("VerifySig", mock.Anything, mock.Anything, mock.Anything).Return(true)
	orch.On("ServiceURI").Return(url.Parse(uri))
	orch.On("TicketParams", mock.Anything).Return(&net.TicketParams{}, nil)
	orch.On("PriceInfo", mock.Anything).Return(nil, expErr)

	_, err := getOrchestrator(orch, &net.OrchestratorRequest{})

	assert.EqualError(t, err, expErr.Error())
}

type mockOSSession struct {
	mock.Mock
}

func (s *mockOSSession) SaveData(name string, data []byte) (string, error) {
	args := s.Called()
	return args.String(0), args.Error(1)
}

func (s *mockOSSession) EndSession() {
	s.Called()
}

func (s *mockOSSession) GetInfo() *net.OSInfo {
	args := s.Called()
	if args.Get(0) != nil {
		return args.Get(0).(*net.OSInfo)
	}
	return nil
}

func (s *mockOSSession) IsExternal() bool {
	args := s.Called()
	return args.Bool(0)
}

type mockOrchestrator struct {
	mock.Mock
}

func (o *mockOrchestrator) ServiceURI() *url.URL {
	args := o.Called()
	if args.Get(0) != nil {
		return args.Get(0).(*url.URL)
	}
	return nil
}
func (o *mockOrchestrator) Address() ethcommon.Address {
	o.Called()
	return ethcommon.Address{}
}
func (o *mockOrchestrator) TranscoderSecret() string {
	o.Called()
	return ""
}
func (o *mockOrchestrator) Sign(msg []byte) ([]byte, error) {
	o.Called(msg)
	return nil, nil
}
func (o *mockOrchestrator) VerifySig(addr ethcommon.Address, msg string, sig []byte) bool {
	args := o.Called(addr, msg, sig)
	return args.Bool(0)
}
func (o *mockOrchestrator) CurrentBlock() *big.Int {
	o.Called()
	return nil
}
func (o *mockOrchestrator) TranscodeSeg(md *core.SegTranscodingMetadata, seg *stream.HLSSegment) (*core.TranscodeResult, error) {
	args := o.Called(md, seg)

	var res *core.TranscodeResult
	if args.Get(0) != nil {
		res = args.Get(0).(*core.TranscodeResult)
	}

	return res, args.Error(1)
}
func (o *mockOrchestrator) ServeTranscoder(stream net.Transcoder_RegisterTranscoderServer, capacity int) {
	o.Called(stream)
}
func (o *mockOrchestrator) TranscoderResults(job int64, res *core.RemoteTranscoderResult) {
	o.Called(job, res)
}
func (o *mockOrchestrator) ProcessPayment(payment net.Payment, manifestID core.ManifestID) error {
	args := o.Called(payment, manifestID)
	return args.Error(0)
}

func (o *mockOrchestrator) TicketParams(sender ethcommon.Address) (*net.TicketParams, error) {
	args := o.Called(sender)
	if args.Get(0) != nil {
		return args.Get(0).(*net.TicketParams), args.Error(1)
	}
	return nil, args.Error(1)
}

func (o *mockOrchestrator) PriceInfo(sender ethcommon.Address) (*net.PriceInfo, error) {
	args := o.Called(sender)
	if args.Get(0) != nil {
		return args.Get(0).(*net.PriceInfo), args.Error(1)
	}
	return nil, args.Error(1)
}

func (o *mockOrchestrator) CheckCapacity(mid core.ManifestID) error {
	return nil
}

func (o *mockOrchestrator) SufficientBalance(addr ethcommon.Address, manifestID core.ManifestID) bool {
	args := o.Called(addr, manifestID)
	return args.Bool(0)
}

func (o *mockOrchestrator) DebitFees(addr ethcommon.Address, manifestID core.ManifestID, price *net.PriceInfo, pixels int64) {
	o.Called(addr, manifestID, price, pixels)
}

func defaultTicketParams() *net.TicketParams {
	return &net.TicketParams{
		Recipient:         pm.RandBytes(123),
		FaceValue:         pm.RandBytes(123),
		WinProb:           pm.RandBytes(123),
		RecipientRandHash: pm.RandBytes(123),
		Seed:              pm.RandBytes(123),
	}
}

func defaultPayment(t *testing.T) *net.Payment {
	return defaultPaymentWithTickets(t, []*net.TicketSenderParams{defaultTicketSenderParams(t)})
}

func defaultPaymentWithTickets(t *testing.T, senderParams []*net.TicketSenderParams) *net.Payment {
	sender := pm.RandBytes(123)

	payment := &net.Payment{
		TicketParams:       defaultTicketParams(),
		Sender:             sender,
		TicketSenderParams: senderParams,
		ExpectedPrice: &net.PriceInfo{
			PricePerUnit:  1,
			PixelsPerUnit: 3,
		},
	}
	return payment
}

func defaultTicketSenderParams(t *testing.T) *net.TicketSenderParams {
	return &net.TicketSenderParams{
		SenderNonce: 456,
		Sig:         pm.RandBytes(123),
	}
}
