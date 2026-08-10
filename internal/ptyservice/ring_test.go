// ptyservice Ring 测试：终端输出 replay 的双重有界与 cursor 语义。
//
// 职责：
//   - frame seq 单调且 replay 不重不漏
//   - 同时按 frame 数和原始字节数淘汰
//   - cursor 过旧返回 bounded snapshot 与 through seq
//
// 边界：
//   - 纯内存测试，不启动 shell、不访问 SQLite
package ptyservice

import (
	"bytes"
	"testing"
)

func TestRingReplayAndCursorExpiry(t *testing.T) {
	ring := NewRing(3, 7)
	for _, data := range [][]byte{[]byte("aa"), []byte("bbb"), []byte("cccc"), []byte("d")} {
		ring.Append(data)
	}
	if ring.ThroughSeq() != 4 || ring.FrameCount() > 3 || ring.ByteCount() > 7 {
		t.Fatalf("ring through/frames/bytes = %d/%d/%d", ring.ThroughSeq(), ring.FrameCount(), ring.ByteCount())
	}

	replay, expired, snapshot := ring.Replay(2)
	if expired || len(replay) != 2 || replay[0].Seq != 3 || replay[1].Seq != 4 {
		t.Fatalf("replay after 2 = %+v expired=%v", replay, expired)
	}
	if replay[0].Seq >= replay[1].Seq {
		t.Fatal("replay seq 必须严格递增")
	}

	replay, expired, snapshot = ring.Replay(0)
	if !expired || len(replay) != 0 || snapshot.ThroughSeq != 4 {
		t.Fatalf("expired replay=%+v expired=%v snapshot=%+v", replay, expired, snapshot)
	}
	if !bytes.Equal(snapshot.Data, []byte("ccccd")) {
		t.Fatalf("snapshot data=%q, want retained output", snapshot.Data)
	}
}

func TestRingCopiesCallerBuffers(t *testing.T) {
	ring := NewRing(4, 64)
	data := []byte("safe")
	ring.Append(data)
	data[0] = 'X'
	replay, expired, _ := ring.Replay(0)
	if expired || len(replay) != 1 || string(replay[0].Data) != "safe" {
		t.Fatalf("ring retained caller buffer alias: %+v", replay)
	}
}

func TestRingOversizedFrameKeepsFullLiveDataAndBoundedSnapshot(t *testing.T) {
	ring := NewRing(2, 4)
	live := ring.Append([]byte("abcdefgh"))
	if string(live.Data) != "abcdefgh" {
		t.Fatalf("live output 被 replay 边界截断: %q", live.Data)
	}
	replay, expired, snapshot := ring.Replay(0)
	if expired || snapshot.Data != nil || len(replay) != 1 || string(replay[0].Data) != "efgh" || ring.ByteCount() != 4 {
		t.Fatalf("bounded replay = expired:%t replay:%q snapshot:%q bytes:%d",
			expired, replay[0].Data, snapshot.Data, ring.ByteCount())
	}
}

func TestRingDoesNotCopySnapshotUntilCursorExpires(t *testing.T) {
	ring := NewRing(4, 64)
	ring.Append(bytes.Repeat([]byte("x"), 64))
	_, expired, snapshot := ring.Replay(1)
	if expired || snapshot.Data != nil || snapshot.ThroughSeq != 1 {
		t.Fatalf("current cursor built snapshot: expired=%t snapshot=%+v", expired, snapshot)
	}
}
