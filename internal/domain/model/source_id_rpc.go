package model

var (
	// SourceRPC 通用 RPC 调用（备用）
	SourceRPC = MustParseSourceID("rpc:default:call")

	// SourceRPCCallback Server 处理客户端请求
	SourceRPCCallback = MustParseSourceID("rpc:callback:execute")

	// SourceBroadcast Client 多播响应回调
	SourceBroadcast = MustParseSourceID("rpc:broadcast:callback")

	// SourceRPCClient Client 发送请求
	SourceRPCClient = MustParseSourceID("rpc:client:call")
)
