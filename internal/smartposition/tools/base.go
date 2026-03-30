package tools

import (
	"context"
	"encoding/json"
	"fmt"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// JSONTool 用统一的 JSON 输入输出包装 Eino InvokableTool。
// 当前 Graph 节点以 Go 代码驱动 tool 调用，但数据访问依然统一从 Eino tool 边界进入。
type JSONTool[Req any, Resp any] struct {
	name string
	desc string
	run  func(context.Context, Req) (Resp, error)
}

func NewJSONTool[Req any, Resp any](name, desc string, run func(context.Context, Req) (Resp, error)) *JSONTool[Req, Resp] {
	return &JSONTool[Req, Resp]{
		name: name,
		desc: desc,
		run:  run,
	}
}

func (t *JSONTool[Req, Resp]) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: t.name,
		Desc: t.desc,
	}, nil
}

func (t *JSONTool[Req, Resp]) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...einotool.Option) (string, error) {
	var req Req
	if argumentsInJSON != "" {
		if err := json.Unmarshal([]byte(argumentsInJSON), &req); err != nil {
			return "", fmt.Errorf("%s: unmarshal args: %w", t.name, err)
		}
	}
	resp, err := t.run(ctx, req)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return "", fmt.Errorf("%s: marshal resp: %w", t.name, err)
	}
	return string(raw), nil
}

func InvokeJSON[Req any, Resp any](ctx context.Context, tool einotool.InvokableTool, req Req) (Resp, error) {
	var zero Resp
	body, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	raw, err := tool.InvokableRun(ctx, string(body))
	if err != nil {
		return zero, err
	}
	var resp Resp
	if raw == "" {
		return resp, nil
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return zero, err
	}
	return resp, nil
}
