package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/components/base"
	"github.com/rulego/rulego/utils/maps"
)

const componentType = "resourceOrigin"
const relationProduce = "Produce"

const (
	metadataState = "resourceOriginState"
	metadataKind  = "resourceOriginErrorKind"
	metadataURL   = "resourceOriginUrl"
)

type nodeConfiguration struct {
	Root             string `json:"root" label:"Root or ref:// node" required:"true" ref:"primary"`
	StaticURLPrefix  string `json:"staticUrlPrefix" label:"Static URL prefix" ref:"shared"`
	MaxRetainedBytes int64  `json:"maxRetainedBytes" label:"Maximum retained bytes" ref:"shared"`
	MaxResourceBytes int64  `json:"maxResourceBytes" label:"Maximum bytes per resource" ref:"shared"`
	MaxTTLms         int64  `json:"maxTtlMs" label:"Maximum TTL (ms)" ref:"shared"`
	MaxProductionMs  int64  `json:"maxProductionMs" label:"Maximum production time (ms)" ref:"shared"`
}

type resourceOriginNode struct {
	base.SharedNode[*originManager]
	Config nodeConfiguration
}

func (*resourceOriginNode) Type() string { return componentType }

func (*resourceOriginNode) Def() types.ComponentForm {
	relations := []string{relationProduce, types.Success, types.Failure}
	return types.ComponentForm{
		Type:          componentType,
		Category:      "storage",
		Label:         "resource origin",
		Desc:          "Publish lifecycle-bound transformation outputs through RuleGo static mapping",
		Version:       "0.1.0",
		ComponentKind: types.ComponentKindNative,
		RelationTypes: &relations,
	}
}

func (*resourceOriginNode) New() types.Node { return &resourceOriginNode{} }

func (n *resourceOriginNode) Init(ruleConfig types.Config, configuration types.Configuration) error {
	if err := maps.Map2Struct(configuration, &n.Config); err != nil {
		return errors.New("resourceOrigin: invalid configuration")
	}
	if strings.TrimSpace(n.Config.Root) == "" {
		return errors.New("resourceOrigin: root is required")
	}
	if !strings.HasPrefix(n.Config.Root, types.NodeConfigurationPrefixInstanceId) {
		if strings.TrimSpace(n.Config.StaticURLPrefix) == "" || n.Config.MaxRetainedBytes <= 0 ||
			n.Config.MaxResourceBytes <= 0 || n.Config.MaxTTLms <= 0 || n.Config.MaxProductionMs <= 0 {
			return errors.New("resourceOrigin: static URL and positive limits are required")
		}
		const maxDurationMillis = int64(math.MaxInt64) / int64(time.Millisecond)
		if n.Config.MaxTTLms > maxDurationMillis || n.Config.MaxProductionMs > maxDurationMillis {
			return errors.New("resourceOrigin: duration limit is out of range")
		}
	}
	if err := n.SharedNode.InitWithClose(ruleConfig, n.Type(), n.Config.Root, true,
		func() (*originManager, error) {
			return newOriginManager(managerConfig{
				Root:             n.Config.Root,
				StaticURLPrefix:  n.Config.StaticURLPrefix,
				MaxRetainedBytes: n.Config.MaxRetainedBytes,
				MaxResourceBytes: n.Config.MaxResourceBytes,
				MaxTTL:           time.Duration(n.Config.MaxTTLms) * time.Millisecond,
				MaxProduction:    time.Duration(n.Config.MaxProductionMs) * time.Millisecond,
			})
		},
		func(manager *originManager) error { return manager.Close() }); err != nil {
		return err
	}
	n.SharedNode.BindChain(configuration)
	return nil
}

func (n *resourceOriginNode) OnMsg(ruleContext types.RuleContext, msg types.RuleMsg) {
	manager, err := n.SharedNode.GetSafely()
	if err != nil {
		tellOriginError(ruleContext, msg, err)
		return
	}
	operation, request, err := decodeOperation(msg.GetData())
	if err != nil {
		tellOriginError(ruleContext, msg, &originError{Kind: "invalid_input", Err: err})
		return
	}
	ctx := ruleContext.GetContext()
	if ctx == nil {
		ctx = context.Background()
	}
	var descriptor resourceDescriptor
	produce := false
	switch operation {
	case "acquire":
		value := request.(acquirePayload)
		result, acquireErr := manager.Acquire(ctx, acquireRequest{
			Key: value.Key, Fingerprint: value.Fingerprint,
			ParentResourceID:  value.ParentResourceID,
			TTL:               time.Duration(value.TTLms) * time.Millisecond,
			MaxBytes:          value.MaxBytes,
			ProductionTimeout: time.Duration(value.ProductionTimeoutMs) * time.Millisecond,
		})
		descriptor, produce, err = result.Descriptor, result.Produce, acquireErr
	case "commit":
		value := request.(commitPayload)
		descriptor, err = manager.Commit(commitRequest{
			ResourceID: value.ResourceID, Generation: value.Generation, Entrypoint: value.Entrypoint,
		})
	case "fail":
		value := request.(failPayload)
		descriptor, err = manager.Fail(failRequest{
			ResourceID: value.ResourceID, Generation: value.Generation, Kind: value.Kind,
		})
	case "resolve":
		value := request.(resolvePayload)
		descriptor, err = manager.Resolve(value.ResourceID, value.Member)
	}
	if err != nil {
		tellOriginError(ruleContext, msg, err)
		return
	}
	tellOriginResult(ruleContext, msg, descriptor, produce)
}

func (n *resourceOriginNode) Destroy() { _ = n.SharedNode.Close() }

type acquirePayload struct {
	Operation           string `json:"operation"`
	Key                 string `json:"key"`
	Fingerprint         string `json:"fingerprint"`
	ParentResourceID    string `json:"parentResourceId,omitempty"`
	TTLms               int64  `json:"ttlMs"`
	MaxBytes            int64  `json:"maxBytes"`
	ProductionTimeoutMs int64  `json:"productionTimeoutMs"`
}

type commitPayload struct {
	Operation  string `json:"operation"`
	ResourceID string `json:"resourceId"`
	Generation string `json:"generation"`
	Entrypoint string `json:"entrypoint"`
}

type failPayload struct {
	Operation  string `json:"operation"`
	ResourceID string `json:"resourceId"`
	Generation string `json:"generation"`
	Kind       string `json:"kind"`
}

type resolvePayload struct {
	Operation  string `json:"operation"`
	ResourceID string `json:"resourceId"`
	Member     string `json:"member,omitempty"`
}

func decodeOperation(data string) (string, any, error) {
	var head struct {
		Operation string `json:"operation"`
	}
	if err := json.Unmarshal([]byte(data), &head); err != nil {
		return "", nil, err
	}
	var target any
	switch head.Operation {
	case "acquire":
		target = &acquirePayload{}
	case "commit":
		target = &commitPayload{}
	case "fail":
		target = &failPayload{}
	case "resolve":
		target = &resolvePayload{}
	default:
		return "", nil, errors.New("unsupported operation")
	}
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return "", nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return "", nil, errors.New("multiple JSON values")
		}
		return "", nil, err
	}
	switch value := target.(type) {
	case *acquirePayload:
		const maxDurationMillis = int64(math.MaxInt64) / int64(time.Millisecond)
		if value.TTLms <= 0 || value.ProductionTimeoutMs <= 0 ||
			value.TTLms > maxDurationMillis || value.ProductionTimeoutMs > maxDurationMillis {
			return "", nil, errors.New("duration is out of range")
		}
		return head.Operation, *value, nil
	case *commitPayload:
		return head.Operation, *value, nil
	case *failPayload:
		return head.Operation, *value, nil
	case *resolvePayload:
		return head.Operation, *value, nil
	default:
		panic("unreachable operation payload")
	}
}

func tellOriginResult(ruleContext types.RuleContext, input types.RuleMsg, descriptor resourceDescriptor, produce bool) {
	payload, _ := json.Marshal(descriptor)
	output := input.Copy()
	output.DataType = types.JSON
	output.SetBytes(payload)
	output.Metadata.PutValue(metadataState, string(descriptor.State))
	output.Metadata.Delete(metadataKind)
	if descriptor.URL == "" {
		output.Metadata.Delete(metadataURL)
	} else {
		output.Metadata.PutValue(metadataURL, descriptor.URL)
	}
	if produce {
		ruleContext.TellNext(output, relationProduce)
		return
	}
	ruleContext.TellSuccess(output)
}

func tellOriginError(ruleContext types.RuleContext, input types.RuleMsg, err error) {
	kind := "internal"
	var typed *originError
	if errors.As(err, &typed) {
		kind = typed.Kind
	}
	payload, _ := json.Marshal(map[string]string{"state": "error", "kind": kind})
	output := input.Copy()
	output.DataType = types.JSON
	output.SetBytes(payload)
	output.Metadata.PutValue(metadataState, "error")
	output.Metadata.PutValue(metadataKind, kind)
	output.Metadata.Delete(metadataURL)
	ruleContext.TellFailure(output, err)
}
