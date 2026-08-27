package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rulego/rulego"
	"github.com/rulego/rulego/api/types"
	"github.com/rulego/rulego/api/types/endpoint"
)

func TestPluginContract(t *testing.T) {
	components := Plugins.Components()
	if len(components) != 1 || components[0].Type() != componentType {
		t.Fatalf("components = %#v", components)
	}
	wantRelations := []string{relationProduce, types.Success, types.Failure}
	if got := *components[0].(*resourceOriginNode).Def().RelationTypes; !reflect.DeepEqual(got, wantRelations) {
		t.Fatalf("relations = %#v, want %#v", got, wantRelations)
	}
}

func TestResponseProjection(t *testing.T) {
	msg := types.NewMsgWithJsonData(`{"state":"ready","url":"/resources/id/index.m3u8"}`)
	msg.Metadata.PutValue(metadataState, string(stateReady))
	msg.Metadata.PutValue(metadataURL, "/resources/id/index.m3u8")
	out := newOriginTestMessage(&msg)
	if !projectOriginResponse(nil, &endpoint.Exchange{Out: out}) {
		t.Fatal("processor did not handle response")
	}
	if out.status != http.StatusTemporaryRedirect || out.headers.Get("Location") != "/resources/id/index.m3u8" {
		t.Fatalf("ready response status=%d location=%q", out.status, out.headers.Get("Location"))
	}

	msg = types.NewMsgWithJsonData(`{"state":"error","kind":"storage_limit"}`)
	msg.Metadata.PutValue(metadataKind, "storage_limit")
	out = newOriginTestMessage(&msg)
	out.err = errors.New("storage limit")
	projectOriginResponse(nil, &endpoint.Exchange{Out: out})
	if out.status != http.StatusInsufficientStorage {
		t.Fatalf("error response status=%d", out.status)
	}
}

func TestRuleGoSharedOwnerAcquireAndCommit(t *testing.T) {
	if err := rulego.Registry.Register(&resourceOriginNode{}); err != nil {
		t.Fatal(err)
	}
	defer rulego.Registry.Unregister(componentType)
	root := t.TempDir()
	dsl := fmt.Sprintf(`{
  "ruleChain":{"id":"origin-test","root":true},
  "metadata":{
    "firstNodeIndex":1,
    "nodes":[
      {"id":"owner","type":"resourceOrigin","configuration":{
        "root":%q,"staticUrlPrefix":"/resources","maxRetainedBytes":4096,
        "maxResourceBytes":2048,"maxTtlMs":60000,"maxProductionMs":30000}},
      {"id":"origin","type":"resourceOrigin","configuration":{"root":"ref://owner"}},
      {"id":"end","type":"end","configuration":{}}
    ],
    "connections":[
      {"fromId":"origin","toId":"end","type":"Produce"},
      {"fromId":"origin","toId":"end","type":"Success"},
      {"fromId":"origin","toId":"end","type":"Failure"}
    ]
  }
}`, root)
	pool := rulego.NewRuleGo()
	engine, err := pool.New("origin-test", []byte(dsl), rulego.WithConfig(rulego.NewConfig()))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Stop(nil)

	call := func(data string) (types.RuleMsg, string, error) {
		var output types.RuleMsg
		var relation string
		var callbackErr error
		engine.OnMsgAndWait(types.NewMsgWithJsonData(data), types.WithOnEnd(func(_ types.RuleContext, msg types.RuleMsg, err error, gotRelation string) {
			output, relation, callbackErr = msg, gotRelation, err
		}))
		return output, relation, callbackErr
	}
	output, relation, err := call(`{"operation":"acquire","key":"asset","fingerprint":"v1","ttlMs":30000,"maxBytes":1024,"productionTimeoutMs":10000}`)
	if err != nil || relation != relationProduce {
		t.Fatalf("acquire relation=%q err=%v body=%s", relation, err, output.GetData())
	}
	var pending resourceDescriptor
	if err := json.Unmarshal(output.GetBytes(), &pending); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending.StagingDir, "artifact.bin"), []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	commit, _ := json.Marshal(commitPayload{
		Operation: "commit", ResourceID: pending.ResourceID,
		Generation: pending.Generation, Entrypoint: "artifact.bin",
	})
	output, relation, err = call(string(commit))
	if err != nil || relation != types.Success {
		t.Fatalf("commit relation=%q err=%v body=%s", relation, err, output.GetData())
	}
	var ready resourceDescriptor
	if err := json.Unmarshal(output.GetBytes(), &ready); err != nil || ready.State != stateReady || ready.URL == "" {
		t.Fatalf("ready = %#v, %v", ready, err)
	}
}

type originTestMessage struct {
	body    []byte
	headers textproto.MIMEHeader
	msg     *types.RuleMsg
	err     error
	status  int
	written bool
}

func newOriginTestMessage(msg *types.RuleMsg) *originTestMessage {
	return &originTestMessage{headers: make(textproto.MIMEHeader), msg: msg}
}

func (m *originTestMessage) Body() []byte                  { return m.body }
func (m *originTestMessage) Headers() textproto.MIMEHeader { return m.headers }
func (m *originTestMessage) From() string                  { return "" }
func (m *originTestMessage) GetParam(string) string        { return "" }
func (m *originTestMessage) SetMsg(msg *types.RuleMsg)     { m.msg = msg }
func (m *originTestMessage) GetMsg() *types.RuleMsg        { return m.msg }
func (m *originTestMessage) SetStatusCode(status int) {
	if !m.written {
		m.status, m.written = status, true
	}
}
func (m *originTestMessage) SetBody(body []byte) {
	if !m.written {
		m.status, m.written = http.StatusOK, true
	}
	m.body = append(m.body, body...)
}
func (m *originTestMessage) SetError(err error) { m.err = err }
func (m *originTestMessage) GetError() error    { return m.err }
