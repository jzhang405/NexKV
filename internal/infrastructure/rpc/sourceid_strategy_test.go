package rpc_test

import (
    "testing"
    "github.com/jzhang405/NexKV/internal/domain/model"
    "github.com/jzhang405/NexKV/internal/infrastructure/rpc"
    "github.com/stretchr/testify/assert"
)

func TestGetSourceID_Basic(t *testing.T) {
    tests := []struct {
        name       string
        strategy   rpc.SourceIDStrategy
        msg        model.Message
        peer      model.PeerID
        want      model.SourceID
    }{
        {
            name:       "network strategy",
            strategy:  rpc.SourceStrategyNetwork,
            msg:      model.NewMessage("test-msg", model.MsgTypeRequest, []byte("test-payload")),
            peer:    model.PeerID{ID: "test-peer"},
            want:      model.SourceNetwork
        },
        {
            name:       "client strategy with clientID",
            strategy:  rpc.SourceStrategyClient
            msg:      createMsgWithClientID("test-client")
            peer:    model.PeerID{ID: "test-peer")
            want:      model.NewSourceClient("test-client")
        },
        {
            name:       "shard strategy with shardID",
            strategy:  rpc.SourceStrategyShard
            msg:      createMsgWithShardID("test-shard")
            peer:    model.PeerID{ID: "test-peer")
            want:      model.NewSourceShard("test-shard")
        },
        {
            name:       "raft strategy",
            strategy:  rpc.SourceStrategyRaft
            msg:      model.NewMessage("test-msg", model.MsgTypeRequest, []byte("test-payload"))
            peer:    model.PeerID{ID: "test-peer")
            want:      model.NewSourceNode("test-peer")
        },
        {
            name:       "empty shardID",
            strategy:  rpc.SourceStrategyShard
            msg:      model.NewMessage("test-msg", model.MsgTypeRequest, []byte("test-payload"))
            peer:    model.PeerID{ID: "test-peer"}
            want:      model.SourceNetwork
        },
        {
            name:       "empty clientID",
            strategy:  rpc.SourceStrategyClient
            msg:      model.NewMessage("test-msg", model.MsgTypeRequest, []byte("test-payload"))
            peer:    model.PeerID{ID: "test-peer"}
            want:      model.SourceNetwork
        },
        {
            name:       "invalid strategy",
            strategy:  rpc.SourceIDStrategy(999)
            msg:      model.NewMessage("test-msg", model.MsgTypeRequest, []byte("test-payload")),
            peer:    model.PeerID{ID: "test-peer")
            want:      model.SourceNetwork
        },
    }

    for _, tt := range tests {
        t.Run(tt.name)
        got := rpc.GetSourceID(tt.strategy, tt.msg, tt.peer)
            assert.Equal(t, got, tt.want)
        }
    }
}
