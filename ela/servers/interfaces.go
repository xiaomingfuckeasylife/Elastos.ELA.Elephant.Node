package servers

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/elastos/Elastos.ELA.Elephant.Node/ela/blockchain"
	"math"
	"time"

	"github.com/elastos/Elastos.ELA.Elephant.Node/ela/pow"
	aux "github.com/elastos/Elastos.ELA/auxpow"
	chain "github.com/elastos/Elastos.ELA/blockchain"
	"github.com/elastos/Elastos.ELA/common"
	"github.com/elastos/Elastos.ELA/common/config"
	"github.com/elastos/Elastos.ELA/common/log"
	"github.com/elastos/Elastos.ELA/core/contract"
	. "github.com/elastos/Elastos.ELA/core/types"
	"github.com/elastos/Elastos.ELA/core/types/outputpayload"
	. "github.com/elastos/Elastos.ELA/core/types/payload"
	. "github.com/elastos/Elastos.ELA/errors"
	"github.com/elastos/Elastos.ELA/p2p/msg"
	. "github.com/elastos/Elastos.ELA/protocol"
)

const (
	AUXBLOCK_GENERATED_INTERVAL_SECONDS = 5

	MixedUTXO  utxoType = 0x00
	VoteUTXO   utxoType = 0x01
	NormalUTXO utxoType = 0x02
)

var ServerNode Noder
var LocalPow *pow.PowService

var preChainHeight uint64
var preTime int64
var currentAuxBlock *Block

type utxoType byte

func ToReversedString(hash common.Uint256) string {
	return common.BytesToHexString(common.BytesReverse(hash[:]))
}

func FromReversedString(reversed string) ([]byte, error) {
	bytes, err := common.HexStringToBytes(reversed)
	return common.BytesReverse(bytes), err
}

func GetTransactionInfo(header *Header, tx *Transaction) *TransactionInfo {
	inputs := make([]InputInfo, len(tx.Inputs))
	for i, v := range tx.Inputs {
		inputs[i].TxID = ToReversedString(v.Previous.TxID)
		inputs[i].VOut = v.Previous.Index
		inputs[i].Sequence = v.Sequence
	}

	outputs := make([]OutputInfo, len(tx.Outputs))
	for i, v := range tx.Outputs {
		outputs[i].Value = v.Value.String()
		outputs[i].Index = uint32(i)
		address, _ := v.ProgramHash.ToAddress()
		outputs[i].Address = address
		outputs[i].AssetID = ToReversedString(v.AssetID)
		outputs[i].OutputLock = v.OutputLock
		outputs[i].OutputType = uint32(v.OutputType)
		outputs[i].OutputPayload = getOutputPayloadInfo(v.OutputPayload)
	}

	attributes := make([]AttributeInfo, len(tx.Attributes))
	for i, v := range tx.Attributes {
		attributes[i].Usage = v.Usage
		attributes[i].Data = common.BytesToHexString(v.Data)
	}

	programs := make([]ProgramInfo, len(tx.Programs))
	for i, v := range tx.Programs {
		programs[i].Code = common.BytesToHexString(v.Code)
		programs[i].Parameter = common.BytesToHexString(v.Parameter)
	}

	var txHash = tx.Hash()
	var txHashStr = ToReversedString(txHash)
	var size = uint32(tx.GetSize())
	var blockHash string
	var confirmations uint32
	var time uint32
	var blockTime uint32
	if header != nil {
		confirmations = chain.DefaultLedger.Blockchain.GetBestHeight() - header.Height + 1
		blockHash = ToReversedString(header.Hash())
		time = header.Timestamp
		blockTime = header.Timestamp
	}

	return &TransactionInfo{
		TxID:           txHashStr,
		Hash:           txHashStr,
		Size:           size,
		VSize:          size,
		Version:        tx.Version,
		LockTime:       tx.LockTime,
		Inputs:         inputs,
		Outputs:        outputs,
		BlockHash:      blockHash,
		Confirmations:  confirmations,
		Time:           time,
		BlockTime:      blockTime,
		TxType:         tx.TxType,
		PayloadVersion: tx.PayloadVersion,
		Payload:        getPayloadInfo(tx.Payload),
		Attributes:     attributes,
		Programs:       programs,
	}
}

// Input JSON string examples for getblock method as following:
func GetRawTransaction(param Params) map[string]interface{} {
	str, ok := param.String("txid")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	hex, err := FromReversedString(str)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	var hash common.Uint256
	err = hash.Deserialize(bytes.NewReader(hex))
	if err != nil {
		return ResponsePack(InvalidTransaction, "")
	}

	var header *Header
	var targetTransaction *Transaction
	tx, height, err := chain.DefaultLedger.Store.GetTransaction(hash)
	if err != nil {
		//try to find transaction in transaction pool.
		targetTransaction, ok = ServerNode.GetTransactionPool(false)[hash]
		if !ok {
			return ResponsePack(UnknownTransaction,
				"cannot find transaction in blockchain and transactionpool")
		}
	} else {
		targetTransaction = tx
		bHash, err := chain.DefaultLedger.Store.GetBlockHash(height)
		if err != nil {
			return ResponsePack(UnknownTransaction, "")
		}
		header, err = chain.DefaultLedger.Store.GetHeader(bHash)
		if err != nil {
			return ResponsePack(UnknownTransaction, "")
		}
	}

	verbose, _ := param.Bool("verbose")
	if verbose {
		return ResponsePack(Success, GetTransactionInfo(header, targetTransaction))
	} else {
		buf := new(bytes.Buffer)
		targetTransaction.Serialize(buf)
		return ResponsePack(Success, common.BytesToHexString(buf.Bytes()))
	}
}

func GetNeighbors(param Params) map[string]interface{} {
	return ResponsePack(Success, ServerNode.GetNeighbourAddresses())
}

func GetNodeState(param Params) map[string]interface{} {
	nodes := ServerNode.GetNeighborNodes()
	neighbors := make([]Neighbor, 0, len(nodes))
	for _, node := range nodes {
		neighbors = append(neighbors, Neighbor{
			ID:         node.ID(),
			HexID:      fmt.Sprintf("0x%x", node.ID()),
			Height:     node.Height(),
			Services:   node.Services(),
			Relay:      node.IsRelay(),
			External:   node.IsExternal(),
			State:      node.State().String(),
			NetAddress: node.NetAddress().String(),
		})
	}
	nodeState := NodeState{
		Compile:     config.Version,
		ID:          ServerNode.ID(),
		HexID:       fmt.Sprintf("0x%x", ServerNode.ID()),
		Height:      uint64(chain.DefaultLedger.Blockchain.BlockHeight),
		Version:     ServerNode.Version(),
		Services:    ServerNode.Services(),
		Relay:       ServerNode.IsRelay(),
		TxnCnt:      ServerNode.GetTxnCnt(),
		RxTxnCnt:    ServerNode.GetRxTxnCnt(),
		Port:        config.Parameters.NodePort,
		PRCPort:     uint16(config.Parameters.HttpJsonPort),
		RestPort:    uint16(config.Parameters.HttpRestPort),
		WSPort:      uint16(config.Parameters.HttpWsPort),
		OpenPort:    config.Parameters.NodeOpenPort,
		OpenService: config.Parameters.OpenService,
		Neighbors:   neighbors,
	}
	return ResponsePack(Success, nodeState)
}

func SetLogLevel(param Params) map[string]interface{} {
	level, ok := param.Int("level")
	if !ok || level < 0 {
		return ResponsePack(InvalidParams, "level must be an integer in 0-6")
	}

	log.SetPrintLevel(uint8(level))
	return ResponsePack(Success, fmt.Sprint("log level has been set to ", level))
}

func SubmitAuxBlock(param Params) map[string]interface{} {
	blockHashHex, ok := param.String("blockhash")
	if !ok {
		return ResponsePack(InvalidParams, "parameter blockhash not found")
	}
	auxPow, ok := param.String("auxpow")
	if !ok {
		return ResponsePack(InvalidParams, "parameter auxpow not found")
	}

	blockHash, err := common.Uint256FromHexString(blockHashHex)
	if err != nil {
		return ResponsePack(InvalidParams, "bad blockhash")
	}
	var msgAuxBlock *Block
	if msgAuxBlock, ok = LocalPow.AuxBlockPool.GetBlock(*blockHash); !ok {
		log.Debug("[json-rpc:SubmitAuxBlock] block hash unknown", blockHash)
		return ResponsePack(InternalError, "block hash unknown")
	}

	localHeight := chain.DefaultLedger.GetLocalBlockChainHeight()
	if localHeight >= msgAuxBlock.Height {
		return ResponsePack(InternalError, "reject the block which have existing height")
	}

	var aux aux.AuxPow
	buf, _ := common.HexStringToBytes(auxPow)
	if err := aux.Deserialize(bytes.NewReader(buf)); err != nil {
		log.Debug("[json-rpc:SubmitAuxBlock] auxpow deserialization failed", auxPow)
		return ResponsePack(InternalError, "auxpow deserialization failed")
	}

	msgAuxBlock.Header.AuxPow = aux
	_, _, err = chain.DefaultLedger.HeightVersions.AddDposBlock(&DposBlock{
		BlockFlag: true,
		Block:     msgAuxBlock,
	})
	if err != nil {
		log.Debug(err)
		return ResponsePack(InternalError, "adding block failed")
	}

	LocalPow.BroadcastBlock(msgAuxBlock)

	log.Debug("AddBlock called finished and LocalPow.MsgBlock.MapNewBlock has been deleted completely")
	log.Info(auxPow, blockHash)
	return ResponsePack(Success, true)
}

func CreateAuxBlock(param Params) map[string]interface{} {
	var ok bool
	LocalPow.PayToAddr, ok = param.String("paytoaddress")
	if !ok {
		return ResponsePack(InvalidParams, "parameter paytoaddress not found")
	}

	if ServerNode.Height() == 0 || preChainHeight != ServerNode.Height() ||
		time.Now().Unix()-preTime > AUXBLOCK_GENERATED_INTERVAL_SECONDS {

		if preChainHeight != ServerNode.Height() {
			// Clear old blocks since they're obsolete now.
			currentAuxBlock = nil
			LocalPow.AuxBlockPool.ClearBlock()
		}

		// Create new block with nonce = 0
		auxBlock, err := LocalPow.GenerateBlock(config.Parameters.PowConfiguration.PayToAddr, chain.DefaultLedger.Blockchain.BestChain)
		if nil != err {
			return ResponsePack(InternalError, "generate block failed")
		}

		// Update state only when CreateNewBlock succeeded
		preChainHeight = ServerNode.Height()
		preTime = time.Now().Unix()

		// Save
		currentAuxBlock = auxBlock
		LocalPow.AuxBlockPool.AppendBlock(auxBlock)
	}

	// At this point, currentAuxBlock is always initialised: If we make it here without creating
	// a new block above, it means that, in particular, preChainHeight == ServerNode.Height().
	// But for that to happen, we must already have created a currentAuxBlock in a previous call,
	// as preChainHeight is initialised only when currentAuxBlock is.
	if currentAuxBlock == nil {
		return ResponsePack(InternalError, "no block cached")
	}

	type AuxBlock struct {
		ChainID           int            `json:"chainid"`
		Height            uint64         `json:"height"`
		CoinBaseValue     common.Fixed64 `json:"coinbasevalue"`
		Bits              string         `json:"bits"`
		Hash              string         `json:"hash"`
		PreviousBlockHash string         `json:"previousblockhash"`
	}

	SendToAux := AuxBlock{
		ChainID:           aux.AuxPowChainID,
		Height:            ServerNode.Height(),
		CoinBaseValue:     currentAuxBlock.Transactions[0].Outputs[1].Value,
		Bits:              fmt.Sprintf("%x", currentAuxBlock.Header.Bits),
		Hash:              currentAuxBlock.Hash().String(),
		PreviousBlockHash: chain.DefaultLedger.Blockchain.CurrentBlockHash().String(),
	}
	return ResponsePack(Success, &SendToAux)
}

func GetInfo(param Params) map[string]interface{} {
	_, count := ServerNode.GetConnectionCount()
	RetVal := struct {
		Version       int    `json:"version"`
		Balance       int    `json:"balance"`
		Blocks        uint64 `json:"blocks"`
		Timeoffset    int    `json:"timeoffset"`
		Connections   uint   `json:"connections"`
		Testnet       bool   `json:"testnet"`
		Keypoololdest int    `json:"keypoololdest"`
		Keypoolsize   int    `json:"keypoolsize"`
		UnlockedUntil int    `json:"unlocked_until"`
		Paytxfee      int    `json:"paytxfee"`
		Relayfee      int    `json:"relayfee"`
		Errors        string `json:"errors"`
	}{
		Version:       config.Parameters.Version,
		Balance:       0,
		Blocks:        ServerNode.Height(),
		Timeoffset:    0,
		Connections:   count,
		Keypoololdest: 0,
		Keypoolsize:   0,
		UnlockedUntil: 0,
		Paytxfee:      0,
		Relayfee:      0,
		Errors:        "Tobe written"}
	return ResponsePack(Success, &RetVal)
}

func AuxHelp(param Params) map[string]interface{} {

	//TODO  and description for this rpc-interface
	return ResponsePack(Success, "createauxblock==submitauxblock")
}

func ToggleMining(param Params) map[string]interface{} {
	mining, ok := param.Bool("mining")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	var message string
	if mining {
		go LocalPow.Start()
		message = "mining started"
	} else {
		go LocalPow.Halt()
		message = "mining stopped"
	}

	return ResponsePack(Success, message)
}

func DiscreteMining(param Params) map[string]interface{} {

	if LocalPow == nil {
		return ResponsePack(PowServiceNotStarted, "")
	}
	count, ok := param.Uint("count")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	ret := make([]string, 0)

	blockHashes, err := LocalPow.DiscreteMining(uint32(count))
	if err != nil {
		return ResponsePack(Error, err)
	}

	for _, hash := range blockHashes {
		retStr := ToReversedString(*hash)
		ret = append(ret, retStr)
	}

	return ResponsePack(Success, ret)
}

func GetConnectionCount(param Params) map[string]interface{} {
	_, count := ServerNode.GetConnectionCount()
	return ResponsePack(Success, count)
}

func GetTransactionPool(param Params) map[string]interface{} {
	txs := make([]*TransactionInfo, 0)
	for _, t := range ServerNode.GetTransactionPool(false) {
		txs = append(txs, GetTransactionInfo(nil, t))
	}
	return ResponsePack(Success, txs)
}

func GetBlockInfo(block *Block, verbose bool) BlockInfo {
	var txs []interface{}
	if verbose {
		for _, tx := range block.Transactions {
			txs = append(txs, GetTransactionInfo(&block.Header, tx))
		}
	} else {
		for _, tx := range block.Transactions {
			txs = append(txs, ToReversedString(tx.Hash()))
		}
	}
	var versionBytes [4]byte
	binary.BigEndian.PutUint32(versionBytes[:], block.Header.Version)

	var chainWork [4]byte
	binary.BigEndian.PutUint32(chainWork[:], chain.DefaultLedger.Blockchain.GetBestHeight()-block.Header.Height)

	nextBlockHash, _ := chain.DefaultLedger.Store.GetBlockHash(block.Header.Height + 1)

	auxPow := new(bytes.Buffer)
	block.Header.AuxPow.Serialize(auxPow)

	return BlockInfo{
		Hash:              ToReversedString(block.Hash()),
		Confirmations:     chain.DefaultLedger.Blockchain.GetBestHeight() - block.Header.Height + 1,
		StrippedSize:      uint32(block.GetSize()),
		Size:              uint32(block.GetSize()),
		Weight:            uint32(block.GetSize() * 4),
		Height:            block.Header.Height,
		Version:           block.Header.Version,
		VersionHex:        common.BytesToHexString(versionBytes[:]),
		MerkleRoot:        ToReversedString(block.Header.MerkleRoot),
		Tx:                txs,
		Time:              block.Header.Timestamp,
		MedianTime:        block.Header.Timestamp,
		Nonce:             block.Header.Nonce,
		Bits:              block.Header.Bits,
		Difficulty:        chain.CalcCurrentDifficulty(block.Header.Bits),
		ChainWork:         common.BytesToHexString(chainWork[:]),
		PreviousBlockHash: ToReversedString(block.Header.Previous),
		NextBlockHash:     ToReversedString(nextBlockHash),
		AuxPow:            common.BytesToHexString(auxPow.Bytes()),
		MinerInfo:         string(block.Transactions[0].Payload.(*PayloadCoinBase).CoinbaseData[:]),
	}
}

func getBlock(hash common.Uint256, verbose uint32) (interface{}, ErrCode) {
	block, err := chain.DefaultLedger.Store.GetBlock(hash)
	if err != nil {
		return "", UnknownBlock
	}
	switch verbose {
	case 0:
		w := new(bytes.Buffer)
		block.Serialize(w)
		return common.BytesToHexString(w.Bytes()), Success
	case 2:
		return GetBlockInfo(block, true), Success
	}
	return GetBlockInfo(block, false), Success
}

func GetBlockByHash(param Params) map[string]interface{} {
	str, ok := param.String("blockhash")
	if !ok {
		return ResponsePack(InvalidParams, "block hash not found")
	}

	var hash common.Uint256
	hashBytes, err := FromReversedString(str)
	if err != nil {
		return ResponsePack(InvalidParams, "invalid block hash")
	}
	if err := hash.Deserialize(bytes.NewReader(hashBytes)); err != nil {
		ResponsePack(InvalidParams, "invalid block hash")
	}

	verbosity, ok := param.Uint("verbosity")
	if !ok {
		verbosity = 1
	}

	result, error := getBlock(hash, verbosity)

	return ResponsePack(error, result)
}

func SendRawTransaction(param Params) map[string]interface{} {
	str, ok := param.String("data")
	if !ok {
		return ResponsePack(InvalidParams, "need a string parameter named data")
	}

	bys, err := common.HexStringToBytes(str)
	if err != nil {
		return ResponsePack(InvalidParams, "hex string to bytes error")
	}
	var txn Transaction
	if err := txn.Deserialize(bytes.NewReader(bys)); err != nil {
		return ResponsePack(InvalidTransaction, err.Error())
	}

	if errCode := VerifyAndSendTx(&txn); errCode != Success {
		return ResponsePack(errCode, errCode.Message())
	}

	return ResponsePack(Success, ToReversedString(txn.Hash()))
}

func GetBlockHeight(param Params) map[string]interface{} {
	return ResponsePack(Success, chain.DefaultLedger.Blockchain.BlockHeight)
}

func GetBestBlockHash(param Params) map[string]interface{} {
	bestHeight := chain.DefaultLedger.Blockchain.BlockHeight
	return GetBlockHash(map[string]interface{}{"height": float64(bestHeight)})
}

func GetBlockCount(param Params) map[string]interface{} {
	return ResponsePack(Success, chain.DefaultLedger.Blockchain.BlockHeight+1)
}

func GetBlockHash(param Params) map[string]interface{} {
	height, ok := param.Uint("height")
	if !ok {
		return ResponsePack(InvalidParams, "height parameter should be a positive integer")
	}

	hash, err := chain.DefaultLedger.Store.GetBlockHash(height)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	return ResponsePack(Success, ToReversedString(hash))
}

func GetBlockTransactions(block *Block) interface{} {
	trans := make([]string, len(block.Transactions))
	for i := 0; i < len(block.Transactions); i++ {
		trans[i] = ToReversedString(block.Transactions[i].Hash())
	}
	type BlockTransactions struct {
		Hash         string
		Height       uint32
		Transactions []string
	}
	b := BlockTransactions{
		Hash:         ToReversedString(block.Hash()),
		Height:       block.Header.Height,
		Transactions: trans,
	}
	return b
}

func GetTransactionsByHeight(param Params) map[string]interface{} {
	height, ok := param.Uint("height")
	if !ok {
		return ResponsePack(InvalidParams, "height parameter should be a positive integer")
	}

	hash, err := chain.DefaultLedger.Store.GetBlockHash(uint32(height))
	if err != nil {
		return ResponsePack(UnknownBlock, "")

	}
	block, err := chain.DefaultLedger.Store.GetBlock(hash)
	if err != nil {
		return ResponsePack(UnknownBlock, "")
	}
	return ResponsePack(Success, GetBlockTransactions(block))
}

func GetBlockByHeight(param Params) map[string]interface{} {
	height, ok := param.Uint("height")
	if !ok {
		return ResponsePack(InvalidParams, "height parameter should be a positive integer")
	}

	hash, err := chain.DefaultLedger.Store.GetBlockHash(uint32(height))
	if err != nil {
		return ResponsePack(UnknownBlock, err.Error())
	}

	result, errCode := getBlock(hash, 2)

	return ResponsePack(errCode, result)
}

func GetArbitratorGroupByHeight(param Params) map[string]interface{} {
	height, ok := param.Uint("height")
	if !ok {
		return ResponsePack(InvalidParams, "height parameter should be a positive integer")
	}

	hash, err := chain.DefaultLedger.Store.GetBlockHash(uint32(height))
	if err != nil {
		return ResponsePack(UnknownBlock, "")
	}

	block, err := chain.DefaultLedger.Store.GetBlock(hash)
	if err != nil {
		return ResponsePack(InternalError, "")
	}

	arbitratorsBytes := chain.DefaultLedger.Arbitrators.GetArbitrators()
	index := int(block.Header.Height) % len(arbitratorsBytes)

	var arbitrators []string
	for _, data := range arbitratorsBytes {
		arbitrators = append(arbitrators, common.BytesToHexString(data))
	}

	result := ArbitratorGroupInfo{
		OnDutyArbitratorIndex: index,
		Arbitrators:           arbitrators,
	}

	return ResponsePack(Success, result)
}

//Asset
func GetAssetByHash(param Params) map[string]interface{} {
	str, ok := param.String("hash")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}
	hashBytes, err := FromReversedString(str)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	var hash common.Uint256
	err = hash.Deserialize(bytes.NewReader(hashBytes))
	if err != nil {
		return ResponsePack(InvalidAsset, "")
	}
	asset, err := chain.DefaultLedger.Store.GetAsset(hash)
	if err != nil {
		return ResponsePack(UnknownAsset, "")
	}
	if false {
		w := new(bytes.Buffer)
		asset.Serialize(w)
		return ResponsePack(Success, common.BytesToHexString(w.Bytes()))
	}
	return ResponsePack(Success, asset)
}

func GetBalanceByAddr(param Params) map[string]interface{} {
	str, ok := param.String("addr")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	programHash, err := common.Uint168FromAddress(str)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	unspends, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
	var balance common.Fixed64 = 0
	for _, u := range unspends {
		for _, v := range u {
			balance = balance + v.Value
		}
	}
	return ResponsePack(Success, balance.String())
}

func GetBalanceByAsset(param Params) map[string]interface{} {
	addr, ok := param.String("addr")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	programHash, err := common.Uint168FromAddress(addr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	assetIDStr, ok := param.String("assetid")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}
	assetIDBytes, err := FromReversedString(assetIDStr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	assetID, err := common.Uint256FromBytes(assetIDBytes)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	unspents, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
	var balance common.Fixed64 = 0
	for k, u := range unspents {
		for _, v := range u {
			if assetID.IsEqual(k) {
				balance = balance + v.Value
			}
		}
	}
	return ResponsePack(Success, balance.String())
}

func GetReceivedByAddress(param Params) map[string]interface{} {
	address, ok := param.String("address")
	if !ok {
		return ResponsePack(InvalidParams, "need a parameter named address")
	}
	programHash, err := common.Uint168FromAddress(address)
	if err != nil {
		return ResponsePack(InvalidParams, "Invalid address: "+address)
	}
	UTXOsWithAssetID, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
	if err != nil {
		return ResponsePack(InvalidParams, err)
	}
	UTXOs := UTXOsWithAssetID[chain.DefaultLedger.Blockchain.AssetID]
	var totalValue common.Fixed64
	for _, unspent := range UTXOs {
		totalValue += unspent.Value
	}

	return ResponsePack(Success, totalValue.String())
}

func ListUnspent(param Params) map[string]interface{} {
	bestHeight := chain.DefaultLedger.Blockchain.GetBestHeight()

	var result []UTXOInfo
	addresses, ok := param.ArrayString("addresses")
	if !ok {
		return ResponsePack(InvalidParams, "need addresses in an array!")
	}
	utxoType := MixedUTXO
	if t, ok := param.String("utxotype"); ok {
		switch t {
		case "mixed":
			utxoType = MixedUTXO
		case "vote":
			utxoType = VoteUTXO
		case "normal":
			utxoType = NormalUTXO
		default:
			return ResponsePack(InvalidParams, "invalid utxotype")
		}
	}
	for _, address := range addresses {
		programHash, err := common.Uint168FromAddress(address)
		if err != nil {
			return ResponsePack(InvalidParams, "Invalid address: "+address)
		}
		unspents, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
		if err != nil {
			return ResponsePack(InvalidParams, "cannot get asset with program")
		}

		for _, unspent := range unspents[chain.DefaultLedger.Blockchain.AssetID] {
			tx, height, err := chain.DefaultLedger.Store.GetTransaction(unspent.TxID)
			if err != nil {
				return ResponsePack(InternalError,
					"unknown transaction "+unspent.TxID.String()+" from persisted utxo")
			}
			if utxoType == VoteUTXO && (tx.Version < TxVersion09 ||
				tx.Version >= TxVersion09 && tx.Outputs[unspent.Index].OutputType != VoteOutput) {
				continue
			}
			if utxoType == NormalUTXO && tx.Version >= TxVersion09 && tx.Outputs[unspent.Index].OutputType == VoteOutput {
				continue
			}
			result = append(result, UTXOInfo{
				TxType:        byte(tx.TxType),
				TxID:          ToReversedString(unspent.TxID),
				AssetID:       ToReversedString(chain.DefaultLedger.Blockchain.AssetID),
				VOut:          unspent.Index,
				Amount:        unspent.Value.String(),
				Address:       address,
				OutputLock:    tx.Outputs[unspent.Index].OutputLock,
				Confirmations: bestHeight - height + 1,
			})
		}
	}
	return ResponsePack(Success, result)
}

func GetUnspends(param Params) map[string]interface{} {
	addr, ok := param.String("addr")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	programHash, err := common.Uint168FromAddress(addr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	type UTXOUnspentInfo struct {
		TxID  string `json:"Txid"`
		Index uint32 `json:"Index"`
		Value string `json:"Value"`
	}
	type Result struct {
		AssetID   string            `json:"AssetId"`
		AssetName string            `json:"AssetName"`
		Utxo      []UTXOUnspentInfo `json:"Utxo"`
	}
	var results []Result
	unspends, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)

	for k, u := range unspends {
		asset, err := chain.DefaultLedger.Store.GetAsset(k)
		if err != nil {
			return ResponsePack(InternalError, "")
		}
		var unspendsInfo []UTXOUnspentInfo
		for _, v := range u {
			unspendsInfo = append(unspendsInfo, UTXOUnspentInfo{ToReversedString(v.TxID), v.Index, v.Value.String()})
		}
		results = append(results, Result{ToReversedString(k), asset.Name, unspendsInfo})
	}
	return ResponsePack(Success, results)
}

func GetUnspendOutput(param Params) map[string]interface{} {
	addr, ok := param.String("addr")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}
	programHash, err := common.Uint168FromAddress(addr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	assetID, ok := param.String("assetid")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}
	bys, err := FromReversedString(assetID)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	var assetHash common.Uint256
	if err := assetHash.Deserialize(bytes.NewReader(bys)); err != nil {
		return ResponsePack(InvalidParams, "")
	}
	type UTXOUnspentInfo struct {
		TxID  string `json:"Txid"`
		Index uint32 `json:"Index"`
		Value string `json:"Value"`
	}
	infos, err := chain.DefaultLedger.Store.GetUnspentFromProgramHash(*programHash, assetHash)
	if err != nil {
		return ResponsePack(InvalidParams, "")

	}
	var UTXOoutputs []UTXOUnspentInfo
	for _, v := range infos {
		UTXOoutputs = append(UTXOoutputs, UTXOUnspentInfo{TxID: ToReversedString(v.TxID), Index: v.Index, Value: v.Value.String()})
	}
	return ResponsePack(Success, UTXOoutputs)
}

//Transaction
func GetTransactionByHash(param Params) map[string]interface{} {
	str, ok := param.String("hash")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}

	bys, err := FromReversedString(str)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	var hash common.Uint256
	err = hash.Deserialize(bytes.NewReader(bys))
	if err != nil {
		return ResponsePack(InvalidTransaction, "")
	}
	txn, height, err := chain.DefaultLedger.Store.GetTransaction(hash)
	if err != nil {
		return ResponsePack(UnknownTransaction, "")
	}
	if false {
		w := new(bytes.Buffer)
		txn.Serialize(w)
		return ResponsePack(Success, common.BytesToHexString(w.Bytes()))
	}
	bHash, err := chain.DefaultLedger.Store.GetBlockHash(height)
	if err != nil {
		return ResponsePack(UnknownBlock, "")
	}
	header, err := chain.DefaultLedger.Store.GetHeader(bHash)
	if err != nil {
		return ResponsePack(UnknownBlock, "")
	}

	return ResponsePack(Success, GetTransactionInfo(header, txn))
}

func GetExistWithdrawTransactions(param Params) map[string]interface{} {
	txsStr, ok := param.String("txs")
	if !ok {
		return ResponsePack(InvalidParams, "txs not found")
	}

	txsBytes, err := common.HexStringToBytes(txsStr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	var txHashes []string
	err = json.Unmarshal(txsBytes, &txHashes)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}

	var resultTxHashes []string
	for _, txHash := range txHashes {
		txHashBytes, err := common.HexStringToBytes(txHash)
		if err != nil {
			return ResponsePack(InvalidParams, "")
		}
		hash, err := common.Uint256FromBytes(txHashBytes)
		if err != nil {
			return ResponsePack(InvalidParams, "")
		}
		inStore := chain.DefaultLedger.Store.IsSidechainTxHashDuplicate(*hash)
		inTxPool := ServerNode.IsDuplicateSidechainTx(*hash)
		if inTxPool || inStore {
			resultTxHashes = append(resultTxHashes, txHash)
		}
	}

	return ResponsePack(Success, resultTxHashes)
}

type Producer struct {
	OwnerPublicKey string `json:"ownerpublickey"`
	NodePublicKey  string `json:"nodepublickey"`
	Nickname       string `json:"nickname"`
	Url            string `json:"url"`
	Location       uint64 `json:"location"`
	Active         bool   `json:"active"`
	Votes          string `json:"votes"`
	NetAddress     string `json:"netaddress"`
	Index          uint64 `json:"index"`
}

type Producers struct {
	Producers   []Producer `json:"producers"`
	TotalVotes  string     `json:"totalvotes"`
	TotalCounts uint64     `json:"totalcounts"`
}

func ListProducers(param Params) map[string]interface{} {
	start, _ := param.Int("start")
	limit, ok := param.Int("limit")
	if !ok {
		limit = math.MaxInt64
	}

	producers, err := chain.DefaultLedger.Store.GetRegisteredProducersSorted()
	if err != nil {
		return ResponsePack(Error, "not found producer")
	}
	var ps []Producer
	for i, p := range producers {
		var active bool
		pk := common.BytesToHexString(p.OwnerPublicKey)
		state := chain.DefaultLedger.Store.GetProducerStatus(pk)
		if state == chain.ProducerRegistered {
			active = true
		}
		vote := chain.DefaultLedger.Store.GetProducerVote(p.OwnerPublicKey)
		producer := Producer{
			OwnerPublicKey: pk,
			NodePublicKey:  common.BytesToHexString(p.NodePublicKey),
			Nickname:       p.NickName,
			Url:            p.Url,
			Location:       p.Location,
			Active:         active,
			Votes:          vote.String(),
			NetAddress:     p.NetAddress,
			Index:          uint64(i),
		}
		ps = append(ps, producer)
	}

	var resultPs []Producer
	var totalVotes common.Fixed64
	for i := start; i < limit && i < int64(len(ps)); i++ {
		resultPs = append(resultPs, ps[i])
	}
	for i := 0; i < len(ps); i++ {
		v, _ := common.StringToFixed64(ps[i].Votes)
		totalVotes += *v
	}

	result := &Producers{
		Producers:   resultPs,
		TotalVotes:  totalVotes.String(),
		TotalCounts: uint64(len(producers)),
	}

	return ResponsePack(Success, result)
}

func ProducerStatus(param Params) map[string]interface{} {
	publicKey, ok := param.String("publickey")
	if !ok {
		return ResponsePack(InvalidParams, "public key not found")
	}
	publicKeyBytes, err := common.HexStringToBytes(publicKey)
	if err != nil {
		return ResponsePack(InvalidParams, "invalid public key")
	}
	if _, err = contract.PublicKeyToStandardProgramHash(publicKeyBytes); err != nil {
		return ResponsePack(InvalidParams, "invalid public key bytes")
	}
	return ResponsePack(Success, chain.DefaultLedger.Store.GetProducerStatus(publicKey))
}

func VoteStatus(param Params) map[string]interface{} {
	address, ok := param.String("address")
	if !ok {
		return ResponsePack(InvalidParams, "address not found")
	}

	programHash, err := common.Uint168FromAddress(address)
	if err != nil {
		return ResponsePack(InvalidParams, "Invalid address: "+address)
	}
	unspents, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
	if err != nil {
		return ResponsePack(InvalidParams, "cannot get asset with program")
	}

	var total common.Fixed64
	var voting common.Fixed64
	for _, unspent := range unspents[chain.DefaultLedger.Blockchain.AssetID] {
		tx, _, err := chain.DefaultLedger.Store.GetTransaction(unspent.TxID)
		if err != nil {
			return ResponsePack(InternalError, "unknown transaction "+unspent.TxID.String()+" from persisted utxo")
		}
		if tx.Outputs[unspent.Index].OutputType == VoteOutput {
			voting += unspent.Value
		}
		total += unspent.Value
	}

	pending := false
	for _, t := range ServerNode.GetTransactionPool(false) {
		for _, i := range t.Inputs {
			tx, _, err := chain.DefaultLedger.Store.GetTransaction(i.Previous.TxID)
			if err != nil {
				return ResponsePack(InternalError, "unknown transaction "+i.Previous.TxID.String()+" from persisted utxo")
			}
			if tx.Outputs[i.Previous.Index].ProgramHash.IsEqual(*programHash) {
				pending = true
			}
		}
		for _, o := range t.Outputs {
			if o.OutputType == VoteOutput && o.ProgramHash.IsEqual(*programHash) {
				pending = true
			}
		}
		if pending {
			break
		}
	}

	type voteInfo struct {
		Total   string `json:"total"`
		Voting  string `json:"voting"`
		Pending bool   `json:"pending"`
	}
	return ResponsePack(Success, &voteInfo{
		Total:   total.String(),
		Voting:  voting.String(),
		Pending: pending,
	})
}

func GetDepositCoin(param Params) map[string]interface{} {
	pk, ok := param.String("ownerpublickey")
	if !ok {
		return ResponsePack(InvalidParams, "need a param called ownerpublickey")
	}
	pkBytes, err := hex.DecodeString(pk)
	if err != nil {
		return ResponsePack(InvalidParams, "invalid publickey")
	}
	programHash, err := contract.PublicKeyToDepositProgramHash(pkBytes)
	if err != nil {
		return ResponsePack(InvalidParams, "invalid publickey to programHash")
	}
	unspends, err := chain.DefaultLedger.Store.GetUnspentsFromProgramHash(*programHash)
	var balance common.Fixed64 = 0
	for _, u := range unspends {
		for _, v := range u {
			balance = balance + v.Value
		}
	}
	var deducted common.Fixed64 = 0
	//todo get deducted coin

	type depositCoin struct {
		Available string `json:"available"`
		Deducted  string `json:"deducted"`
	}
	return ResponsePack(Success, &depositCoin{
		Available: balance.String(),
		Deducted:  deducted.String(),
	})
}

func EstimateSmartFee(param Params) map[string]interface{} {
	confirm, ok := param.Int("confirmations")
	if !ok {
		return ResponsePack(InvalidParams, "need a param called confirmations")
	}
	if confirm > 25 {
		return ResponsePack(InvalidParams, "support only 25 confirmations at most")
	}
	currentHeight := chain.DefaultLedger.GetLocalBlockChainHeight()
	var FeeRate = 10000 //basic fee rate 10000 sela per KB
	var count = 0
	// 6 confirmations at most
	for i := currentHeight; i == 0; i-- {
		b, _ := chain.DefaultLedger.GetBlockWithHeight(i)
		if b.GetSize() < msg.MaxBlockSize {
			break
		} else {
			//how many full blocks continuously before?
			count++
		}
	}

	return ResponsePack(Success, GetFeeRate(count, int(confirm))*FeeRate)
}

func GetHistory(param Params) map[string]interface{} {
	addr, ok := param.String("addr")
	if !ok {
		return ResponsePack(InvalidParams, "")
	}
	_, err := common.Uint168FromAddress(addr)
	if err != nil {
		return ResponsePack(InvalidParams, "")
	}
	txhs := blockchain.DefaultChainStoreEx.GetTxHistory(addr)
	return ResponsePack(Success, txhs)
}

func GetFeeRate(count int, confirm int) int {
	gap := count - confirm
	if gap < 0 {
		gap = -1
	}
	return gap + 2
}

func getPayloadInfo(p Payload) PayloadInfo {
	switch object := p.(type) {
	case *PayloadCoinBase:
		obj := new(CoinbaseInfo)
		obj.CoinbaseData = string(object.CoinbaseData)
		return obj
	case *PayloadRegisterAsset:
		obj := new(RegisterAssetInfo)
		obj.Asset = object.Asset
		obj.Amount = object.Amount.String()
		obj.Controller = common.BytesToHexString(common.BytesReverse(object.Controller.Bytes()))
		return obj
	case *PayloadSideChainPow:
		obj := new(SideChainPowInfo)
		obj.BlockHeight = object.BlockHeight
		obj.SideBlockHash = object.SideBlockHash.String()
		obj.SideGenesisHash = object.SideGenesisHash.String()
		obj.SignedData = common.BytesToHexString(object.SignedData)
		return obj
	case *PayloadWithdrawFromSideChain:
		obj := new(WithdrawFromSideChainInfo)
		obj.BlockHeight = object.BlockHeight
		obj.GenesisBlockAddress = object.GenesisBlockAddress
		for _, hash := range object.SideChainTransactionHashes {
			obj.SideChainTransactionHashes = append(obj.SideChainTransactionHashes, hash.String())
		}
		return obj
	case *PayloadTransferCrossChainAsset:
		obj := new(TransferCrossChainAssetInfo)
		obj.CrossChainAddresses = object.CrossChainAddresses
		obj.OutputIndexes = object.OutputIndexes
		obj.CrossChainAmounts = object.CrossChainAmounts
		return obj
	case *PayloadTransferAsset:
	case *PayloadRecord:
	case *PayloadRegisterProducer:
		obj := new(RegisterProducerInfo)
		obj.OwnerPublicKey = common.BytesToHexString(object.OwnerPublicKey)
		obj.NodePublicKey = common.BytesToHexString(object.NodePublicKey)
		obj.NickName = object.NickName
		obj.Url = object.Url
		obj.Location = object.Location
		obj.NetAddress = object.NetAddress
		obj.Signature = common.BytesToHexString(object.Signature)
		return obj
	case *PayloadCancelProducer:
		obj := new(CancelProducerInfo)
		obj.OwnerPublicKey = common.BytesToHexString(object.OwnerPublicKey)
		obj.Signature = common.BytesToHexString(object.Signature)
		return obj
	case *PayloadUpdateProducer:
		obj := &UpdateProducerInfo{
			new(RegisterProducerInfo),
		}
		obj.OwnerPublicKey = common.BytesToHexString(object.OwnerPublicKey)
		obj.NodePublicKey = common.BytesToHexString(object.NodePublicKey)
		obj.NickName = object.NickName
		obj.Url = object.Url
		obj.Location = object.Location
		obj.NetAddress = object.NetAddress
		obj.Signature = common.BytesToHexString(object.Signature)
		return obj
	}
	return nil
}

func getOutputPayloadInfo(op OutputPayload) OutputPayloadInfo {
	switch object := op.(type) {
	case *outputpayload.DefaultOutput:
		obj := new(DefaultOutputInfo)
		return obj
	case *outputpayload.VoteOutput:
		obj := new(VoteOutputInfo)
		obj.Version = object.Version
		for _, content := range object.Contents {
			var contentInfo VoteContentInfo
			contentInfo.VoteType = content.VoteType
			for _, candidate := range content.Candidates {
				contentInfo.CandidatesInfo = append(contentInfo.CandidatesInfo, common.BytesToHexString(candidate))
			}
			obj.Contents = append(obj.Contents, contentInfo)
		}
		return obj
	}

	return nil
}

func VerifyAndSendTx(txn *Transaction) ErrCode {
	// if transaction is verified unsuccessfully then will not put it into transaction pool
	if errCode := ServerNode.AppendToTxnPool(txn); errCode != Success {
		log.Warn("Can NOT add the transaction to TxnPool")
		log.Info("[httpjsonrpc] VerifyTransaction failed when AppendToTxnPool. Errcode:", errCode)
		return errCode
	}
	if err := ServerNode.Relay(nil, txn); err != nil {
		log.Error("Xmit Tx Error:Relay transaction failed.", err)
		return ErrXmitFail
	}
	return Success
}

func ResponsePack(errCode ErrCode, result interface{}) map[string]interface{} {
	if errCode != 0 && (result == "" || result == nil) {
		result = ErrMap[errCode]
	}
	return map[string]interface{}{"Result": result, "Error": errCode}
}
