#!/usr/bin/env python3
"""Materialize the small pinned Comet fork; never edit Go's module cache."""
import json
import pathlib
import subprocess
import shutil
import os

root = pathlib.Path(__file__).resolve().parents[2]
module = json.loads(subprocess.check_output(
    ["go", "mod", "download", "-json", "github.com/cometbft/cometbft@v0.38.26"], cwd=root))
source = pathlib.Path(module["Dir"])
out = root / ".scratch/comet-src"
if not out.exists():
    shutil.copytree(source, out)
    for parent, dirs, files in os.walk(out):
        os.chmod(parent, 0o755)
        for name in files:
            os.chmod(pathlib.Path(parent) / name, 0o644)

def patch(name, edits):
    text = (source / name).read_text(encoding="utf-8")
    for before, after in edits:
        if text.count(before) != 1:
            raise RuntimeError(f"pinned source mismatch: {name}: {before[:70]}")
        text = text.replace(before, after, 1)
    target = out / name
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text, encoding="utf-8")

patch("types/tx.go", [
    ("return tmhash.Sum(tx)", "return redactionTxHash(tx)"),
    ("return sha256.Sum256(tx)", "var key TxKey; copy(key[:], tx.Hash()); return key"),
    ('\n\t"github.com/cometbft/cometbft/crypto/tmhash"', ''),
])
patch("types/part_set.go", [
    ('type PartSet struct {', 'type PartSet struct {\n redactionSeen bool\n redactionHeight int64\n redactionRevision uint64'),
    ('type Part struct {', 'type Part struct {\n Redaction *PartRedaction `json:"redaction,omitempty"`'),
    ('pb.Bytes = part.Bytes', 'pb.Bytes = encodeRedactionPart(part)'),
    ('part.Bytes = pb.Bytes', 'if err := decodeRedactionPart(part, pb.Bytes); err != nil { return nil, err }'),
    ('if part.Proof.Verify(ps.Hash(), part.Bytes) != nil {',
     'leaf, err := partCommitment(part)\n if err != nil || part.Proof.Verify(ps.Hash(), leaf) != nil {'),
    ('// Add part\n', '// Add part\n if !ps.acceptRedaction(part) { return false, ErrPartSetInvalidProof }\n'),
])
patch("types/block.go", [
    ('return NewPartSetFromData(bz, partSize), nil',
     'if redactionKey != nil { return NewRedactablePartSet(bz, partSize, b.Height, 0, nil) }; return NewPartSetFromData(bz, partSize), nil'),
])
patch("store/store.go", [
    ('type BlockStore struct {', 'type BlockStore struct {\n revisionMtx cmtsync.RWMutex'),
    ('func (bs *BlockStore) LoadBlock(height int64) *types.Block {',
     'func (bs *BlockStore) LoadBlock(height int64) *types.Block {\n bs.revisionMtx.RLock(); defer bs.revisionMtx.RUnlock(); return bs.loadBlock(height)\n}\nfunc (bs *BlockStore) loadBlock(height int64) *types.Block {'),
])
patch("consensus/replay.go", [
    ('block := h.store.LoadBlock(i)', 'block := h.store.LoadBlock(i)\n if original, ok := h.store.(interface { LoadOriginalBlock(int64) (*types.Block,error) }); ok { block, err = original.LoadOriginalBlock(i); if err != nil { return nil, err } }'),
    ('block := h.store.LoadBlock(height)', 'block := h.store.LoadBlock(height)\n if original, ok := h.store.(interface { LoadOriginalBlock(int64) (*types.Block,error) }); ok { var e error; block, e = original.LoadOriginalBlock(height); if e != nil { return sm.State{}, e } }'),
])
patch("blocksync/reactor.go", [
    ('block := bcR.store.LoadBlock(msg.Height)', 'block := bcR.store.LoadBlock(msg.Height)\n if original, ok := bcR.store.(interface { LoadOriginalBlock(int64) (*types.Block,error) }); ok { var e error; block, e = original.LoadOriginalBlock(msg.Height); if e != nil { bcR.Logger.Error("load original block", "err", e); return false } }'),
])
patch("consensus/reactor.go", [
    ('part := conR.conS.blockStore.LoadBlockPart(prs.Height, index)', 'part := conR.conS.blockStore.LoadBlockPart(prs.Height, index)\n if original, ok := conR.conS.blockStore.(interface { OriginalBlockPart(int64,int) (*types.Part,error) }); ok { var e error; part, e = original.OriginalBlockPart(prs.Height,index); if e != nil { logger.Error("load original part", "err", e); return } }'),
    ('if err := m.Part.ValidateBasic(); err != nil {', 'if err := m.Part.ValidateOriginal(m.Height); err != nil { return err }\n if err := m.Part.ValidateBasic(); err != nil {'),
])
patch("node/node.go", [
    (') (*Node, error) {\n\tblockStore, stateDB, err := initDBs(config, dbProvider)\n\tif err != nil {\n\t\treturn nil, err\n\t}', ') (*Node, error) {\n\tblockStore, stateDB, err := initDBs(config, dbProvider)\n\tif err != nil {\n\t\treturn nil, err\n\t}\n if BeforeReplay != nil { if err := BeforeReplay(blockStore); err != nil { return nil, err } }'),
])
# Opt-in timings around the pinned implementation's real synchronous calls.
# The hook is nil in normal runs; no extra log or storage writes are introduced.
trace_import = ('import (', 'import (\n "github.com/cometbft/cometbft/libs/operationtrace"')
patch("libs/flowrate/flowrate.go", [trace_import,
    ('now = m.waitNextSample(now)',
     'endWait := operationtrace.Start("p2p_rate_limit_wait", 0)\n now = m.waitNextSample(now)\n endWait()'),
])
patch("state/store.go", [trace_import,
    ('func (store dbStore) SaveFinalizeBlockResponse(height int64, resp *abci.ResponseFinalizeBlock) error {',
     'func (store dbStore) SaveFinalizeBlockResponse(height int64, resp *abci.ResponseFinalizeBlock) error {\n defer operationtrace.Start("finalize_response_save", height)()\n batch := store.db.NewBatch()\n defer batch.Close()'),
    ('func (store dbStore) Save(state State) error {',
     'func (store dbStore) Save(state State) error {\n defer operationtrace.Start("consensus_state_save", state.LastBlockHeight)()'),
    ('bz, err := resp.Marshal()',
     'endEncode := operationtrace.Start("finalize_history_encode", height)\n bz, err := resp.Marshal()\n endEncode()'),
    ('if err := store.db.Set(calcABCIResponsesKey(height), bz); err != nil {',
     'endSet := operationtrace.Start("finalize_history_batch_set", height)\n setErr := batch.Set(calcABCIResponsesKey(height), bz)\n endSet()\n if err := setErr; err != nil {'),
    ('bz, err := response.Marshal()',
     'endLastEncode := operationtrace.Start("finalize_recovery_encode", height)\n bz, err := response.Marshal()\n endLastEncode()'),
    ('return store.db.SetSync(lastABCIResponseKey, bz)',
     'if err := batch.Set(lastABCIResponseKey, bz); err != nil { return err }\n endSync := operationtrace.Start("finalize_batch_write_sync", height)\n syncErr := batch.WriteSync()\n endSync()\n return syncErr'),
])
patch("state/execution.go", [trace_import,
    ('blockExec.mempool.Lock()\n\tdefer blockExec.mempool.Unlock()',
     'endLock := operationtrace.Start("commit_mempool_lock", block.Height)\n blockExec.mempool.Lock()\n endLock()\n defer blockExec.mempool.Unlock()'),
    ('err := blockExec.mempool.FlushAppConn()',
     'endFlush := operationtrace.Start("commit_mempool_flush", block.Height)\n err := blockExec.mempool.FlushAppConn()\n endFlush()'),
])
patch("libs/autofile/autofile.go", [trace_import,
    ('file *os.File', 'file *os.File\n synced bool // Protected by mtx; invalidated by writes and every reopen.'),
    ('n, err = af.file.Write(b)', 'af.synced = false\n n, err = af.file.Write(b)'),
    ('return af.file.Sync()', '''// No new writes since this handle's last successful sync.
 // Flush still runs in Group, including writes that outgrew its buffer.
 if af.synced {
  defer operationtrace.Start("autofile_sync_skipped", 0)()
  return nil
 }
 defer operationtrace.Start("autofile_sync_needed", 0)()
 err := af.file.Sync()
 af.synced = err == nil
 return err'''),
    ('af.file = file\n', 'af.file = file\n af.synced = false\n'),
])
patch("privval/file.go", [trace_import,
    ('func (lss *FilePVLastSignState) Save() {',
     'func (lss *FilePVLastSignState) Save() {\n defer operationtrace.Start("filepv_save", lss.Height)()'),
    ('jsonBytes, err := cmtjson.MarshalIndent(lss, "", "  ")',
     'endEncode := operationtrace.Start("filepv_encode", lss.Height)\n jsonBytes, err := cmtjson.MarshalIndent(lss, "", "  ")\n endEncode()'),
])
patch("libs/tempfile/tempfile.go", [trace_import,
    ('f, err = os.OpenFile(name, atomicWriteFileFlag, perm)',
     'endOpen := operationtrace.Start("atomic_file_open", 0)\n f, err = os.OpenFile(name, atomicWriteFileFlag, perm)\n endOpen()'),
    ('if n, err := f.Write(data); err != nil {',
     'endWrite := operationtrace.Start("atomic_file_write_sync", 0)\n n, writeErr := f.Write(data)\n endWrite()\n if err := writeErr; err != nil {'),
    ('\tf.Close()\n',
     '\tendClose := operationtrace.Start("atomic_file_close", 0)\n f.Close()\n endClose()\n'),
    ('return os.Rename(f.Name(), filename)',
     'endRename := operationtrace.Start("atomic_file_rename", 0)\n renameErr := os.Rename(f.Name(), filename)\n endRename()\n return renameErr'),
])
patch("consensus/state.go", [trace_import,
    ('block, err = cs.createProposalBlock(context.TODO())',
     'endBuild := operationtrace.Start("proposal_build", height)\n block, err = cs.createProposalBlock(context.TODO())\n endBuild()'),
    ('blockParts, err = block.MakePartSet(types.BlockPartSizeBytes)',
     'endParts := operationtrace.Start("proposal_parts", height)\n blockParts, err = block.MakePartSet(types.BlockPartSizeBytes)\n endParts()'),
    ('if err := cs.wal.FlushAndSync(); err != nil {\n\t\tcs.Logger.Error("failed flushing WAL to disk")\n\t}',
     'endFlush := operationtrace.Start("proposal_wal_flush", height)\n if err := cs.wal.FlushAndSync(); err != nil {\n cs.Logger.Error("failed flushing WAL to disk")\n }\n endFlush()'),
    ('if err := cs.privValidator.SignProposal(cs.state.ChainID, p); err == nil {',
     'endSign := operationtrace.Start("proposal_sign", height)\n signErr := cs.privValidator.SignProposal(cs.state.ChainID,p)\n endSign()\n if err := signErr; err == nil {'),
    ('if err := cs.wal.FlushAndSync(); err != nil {\n\t\treturn nil, err\n\t}',
     'endFlush := operationtrace.Start("vote_wal_flush", cs.Height)\n flushErr := cs.wal.FlushAndSync()\n endFlush()\n if flushErr != nil { return nil, flushErr }'),
    ('recoverable, err := types.SignAndCheckVote(vote, cs.privValidator, cs.state.ChainID, extEnabled && (msgType == cmtproto.PrecommitType))',
     'endSign := operationtrace.Start("vote_sign", cs.Height)\n recoverable, err := types.SignAndCheckVote(vote, cs.privValidator, cs.state.ChainID, extEnabled && (msgType == cmtproto.PrecommitType))\n endSign()'),
    ('''case mi = <-cs.internalMsgQueue:
			err := cs.wal.WriteSync(mi) // NOTE: fsync
			if err != nil {
				panic(fmt.Sprintf(
					"failed to write %v msg to consensus WAL due to %v; check your file system and restart the node",
					mi, err,
				))
			}

			if _, ok := mi.Msg.(*VoteMessage); ok {
				// we actually want to simulate failing during
				// the previous WriteSync, but this isn't easy to do.
				// Equivalent would be to fail here and manually remove
				// some bytes from the end of the wal.
				fail.Fail() // XXX
			}

			// handles proposals, block parts, votes
			cs.handleMsg(mi)''',
     'case mi = <-cs.internalMsgQueue:\n cs.handleInternalMessage(mi)'),
    ('// Save to blockStore.\n', '// Save to blockStore.\n endStore := operationtrace.Start("blockstore_save", height)\n'),
    ('\n\tfail.Fail() // XXX\n\n\t// Write EndHeightMessage', '\n endStore()\n\tfail.Fail() // XXX\n\n\t// Write EndHeightMessage'),
    ('endMsg := EndHeightMessage{height}', 'endEndHeight := operationtrace.Start("endheight_wal", height)\n endMsg := EndHeightMessage{height}'),
    ('\n\tfail.Fail() // XXX\n\n\t// Create a copy of the state', '\n endEndHeight()\n\tfail.Fail() // XXX\n\n\t// Create a copy of the state'),
])

for name in ["types/redaction.go", "store/redaction.go", "node/redaction.go", "libs/operationtrace/trace.go", "libs/autofile/sync_test.go", "consensus/proposal_batch.go", "consensus/proposal_batch_test.go", "state/results_batch_test.go", "store/catchup_test.go"]:
    (out / name).parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(root / "third_party/cometbft" / name, out / name)
subprocess.check_call(["go", "mod", "edit", "-replace",
                      "github.com/cometbft/cometbft=./.scratch/comet-src"], cwd=root)
print(out)
