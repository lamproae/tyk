// +build coprocess
// +build !python
// +build !grpc

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/TykTechnologies/tyk/apidef"
	"github.com/TykTechnologies/tyk/coprocess"
	"github.com/golang/protobuf/proto"
	"github.com/justinas/alice"
)

const baseMiddlewarePath = "middleware/python"

var (
	CoProcessName     = "test"
	MessageType       = coprocess.ProtobufMessage
	testDispatcher, _ = NewCoProcessDispatcher()
)

/* Dispatcher functions */

func TestCoProcessDispatch(t *testing.T) {
	object := &coprocess.Object{
		HookType: coprocess.HookType_Pre,
		HookName: "test",
	}

	messagePtr := testDispatcher.ToCoProcessMessage(object)
	newMessagePtr := testDispatcher.Dispatch(messagePtr)

	newObject := testDispatcher.ToCoProcessObject(newMessagePtr)
	t.Log(newObject)
}

func TestCoProcessDispatchEvent(t *testing.T) {
	spec := createSpecTest(t, basicCoProcessDef)
	remote, _ := url.Parse(spec.Proxy.TargetURL)
	proxy := TykNewSingleHostReverseProxy(remote, spec)
	tykMiddleware := &TykMiddleware{spec, proxy}

	eventMessage := "Auth Failure"
	eventPath := "/"
	eventOrigin := "127.0.0.1"
	eventKey := "abc"

	go tykMiddleware.FireEvent(EVENT_AuthFailure,
		EVENT_AuthFailureMeta{
			EventMetaDefault: EventMetaDefault{Message: eventMessage},
			Path:             eventPath,
			Origin:           eventOrigin,
			Key:              eventKey,
		})

	eventJSON := <-CoProcessDispatchEvent
	eventWrapper := CoProcessEventWrapper{}
	err := json.Unmarshal(eventJSON, &eventWrapper)

	if err != nil {
		t.Fatal(err)
	}

	eventMetadata := eventWrapper.Event.EventMetaData.(map[string]interface{})

	if eventWrapper.Event.EventType != EVENT_AuthFailure {
		err := "Wrong event Type."
		t.Fatal(err)
	}

	if eventMetadata["Message"] != eventMessage || eventMetadata["Path"] != eventPath || eventMetadata["Origin"] != eventOrigin || eventMetadata["Key"] != eventKey {
		err := "Wrong event metadata."
		t.Fatal(err)
	}
}

// Makes sense when testing with -timeout
func TestCoProcessReload(t *testing.T) {
	if testDispatcher == nil {
		testDispatcher, _ = NewCoProcessDispatcher()
	}
	ReloadURLStructure()
	<-CoProcessReload
}

/* Serialization, CP Objects */

func TestCoProcessSerialization(t *testing.T) {
	object := &coprocess.Object{
		HookType: coprocess.HookType_Pre,
		HookName: "test_hook",
	}

	data, err := proto.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}

	messagePtr := testDispatcher.ToCoProcessMessage(object)
	length := testDispatcher.TestMessageLength(messagePtr)

	if len(data) != length {
		err := "The length of the serialized object doesn't match."
		t.Fatal(err)
	}
}

/* Gateway API */

func TestCoProcessGetSetData(t *testing.T) {
	key := "testkey"
	value := "testvalue"
	ttl := 1000

	TestTykStoreData(key, value, ttl)

	retrievedValue := TestTykGetData("testkey")

	if retrievedValue != value {
		err := "Couldn't retrieve key value using CP API"
		t.Fatal(err)
	}

}

func TestCoProcessTykTriggerEvent(t *testing.T) {
	TestTykTriggerEvent("testevent", "testpayload")
}

/* Middleware */

type httpbinGetResponse struct {
	Params  map[string]string `json:"args"`
	Headers map[string]string `json:"headers"`
	Origin  string            `json:"origin"`
	Url     string            `json:"url"`
}

type httpbinHeadersResponse struct {
	Headers map[string]string `json:"headers"`
}

func buildCoProcessChain(spec *APISpec, hookName string, hookType coprocess.HookType, driver apidef.MiddlewareDriver) http.Handler {
	remote, _ := url.Parse(spec.Proxy.TargetURL)
	proxy := TykNewSingleHostReverseProxy(remote, spec)
	proxyHandler := http.HandlerFunc(ProxyHandler(proxy, spec))
	tykMiddleware := &TykMiddleware{spec, proxy}
	mw := CreateCoProcessMiddleware(hookName, hookType, driver, tykMiddleware)
	chain := alice.New(mw).Then(proxyHandler)
	return chain
}

func TestCoProcessMiddleware(t *testing.T) {
	spec := createSpecTest(t, basicCoProcessDef)

	chain := buildCoProcessChain(spec, "hook_test", coprocess.HookType_Pre, apidef.MiddlewareDriver("python"))

	session := createNonThrottledSession()
	spec.SessionManager.UpdateSession("abc", session, 60)

	uri := "/headers"
	method := "GET"

	recorder := httptest.NewRecorder()

	param := make(url.Values)

	req, err := http.NewRequest(method, uri, bytes.NewBufferString(param.Encode()))
	req.Header.Add("authorization", "abc")

	if err != nil {
		t.Fatal(err)
	}

	chain.ServeHTTP(recorder, req)

}

func TestCoProcessObjectPostProcess(t *testing.T) {
	spec := createSpecTest(t, basicCoProcessDef)

	chain := buildCoProcessChain(spec, "hook_test_object_postprocess", coprocess.HookType_Pre, apidef.MiddlewareDriver("python"))

	session := createNonThrottledSession()
	spec.SessionManager.UpdateSession("abc", session, 60)

	uri := "/headers"
	method := "GET"

	recorder := httptest.NewRecorder()

	param := make(url.Values)

	req, err := http.NewRequest(method, uri, bytes.NewBufferString(param.Encode()))
	req.Header.Add("authorization", "abc")
	req.Header.Add("Deletethisheader", "value")

	if err != nil {
		t.Fatal(err)
	}

	chain.ServeHTTP(recorder, req)

	headersResponse := httpbinHeadersResponse{}
	err = json.Unmarshal(recorder.Body.Bytes(), &headersResponse)

	if err != nil {
		t.Fatal(err)
	}

	if headersResponse.Headers["Test"] != "value" {
		t.Fatal("ObjectPostProcess couldn't add a header.")
	}

	if headersResponse.Headers["Deletethisheader"] != "" {
		t.Fatal("ObjectPostProcess couldn't delete a header.")
	}

	recorder = httptest.NewRecorder()

	uri = "/get?a=a_value&b=123&remove=3"
	getReq, err := http.NewRequest(method, uri, bytes.NewBufferString(param.Encode()))
	getReq.Header.Add("authorization", "abc")

	if err != nil {
		t.Fatal(err)
	}

	chain.ServeHTTP(recorder, getReq)

	getResponse := httpbinGetResponse{}
	err = json.Unmarshal(recorder.Body.Bytes(), &getResponse)

	if err != nil {
		t.Fatal(err)
	}

	if getResponse.Params["a"] != "a_value" || getResponse.Params["b"] != "123" {
		t.Fatal("The original parameters don't match.")
	}

	if getResponse.Params["remove"] != "" {
		t.Fatal("ObjectPostProcess couldn't remove a parameter.")
	}

	if getResponse.Params["customparam"] != "customvalue" {
		t.Fatal("ObjectPostProcess couldn't set custom parameters.")
	}

}

/* CP authentication */

func TestCoProcessAuth(t *testing.T) {
	t.Log("CP AUTH")
	spec := createSpecTest(t, protectedCoProcessDef)

	chain := buildCoProcessChain(spec, "hook_test_bad_auth", coprocess.HookType_CustomKeyCheck, apidef.MiddlewareDriver("python"))

	session := createNonThrottledSession()
	spec.SessionManager.UpdateSession("abc", session, 60)

	uri := "/headers"
	method := "GET"

	recorder := httptest.NewRecorder()

	param := make(url.Values)

	req, err := http.NewRequest(method, uri, bytes.NewBufferString(param.Encode()))
	req.Header.Add("authorization", "abc")

	if err != nil {
		t.Fatal(err)
	}

	chain.ServeHTTP(recorder, req)

	if recorder.Code != 403 {
		t.Fatal("Authentication should fail! But it's returning:", recorder.Code)
	}
}

const basicCoProcessDef = `{
	"name": "Tyk Test API - IPCONF Fail",
	"api_id": "1",
	"org_id": "default",
	"definition": {
		"location": "header",
		"key": "version"
	},
	"auth": {
		"auth_header_name": "authorization"
	},
	"version_data": {
		"not_versioned": true,
		"versions": {
			"v1": {
				"name": "v1",
				"expires": "2100-01-02 15:04",
				"use_extended_paths": true,
				"paths": {
					"ignored": [],
					"white_list": [],
					"black_list": []
				}
			}
		}
	},
	"event_handlers": {
		"events": {
			"AuthFailure": [
							{
									"handler_name":"cp_dynamic_handler",
									"handler_meta": {
											"name": "my_handler"
									}
							}
					]
		}
	},
	"custom_middleware": {
		"pre": [
		{
			"name": "MyPreMiddleware",
			"require_session": false
		}
		],
		"driver": "python"
	},
	"proxy": {
		"listen_path": "/v1",
		"target_url": "http://httpbin.org",
		"strip_listen_path": false
	}
}`

const protectedCoProcessDef = `{
	"name": "Tyk Test API",
	"api_id": "1",
	"org_id": "default",
	"definition": {
		"location": "header",
		"key": "version"
	},
	"auth": {
		"auth_header_name": "authorization"
	},
	"enable_coprocess_auth": true,
	"use_keyless": false,
	"version_data": {
		"not_versioned": true,
		"versions": {
			"v1": {
				"name": "v1",
				"expires": "2100-01-02 15:04",
				"use_extended_paths": true,
				"paths": {
					"ignored": [],
					"white_list": [],
					"black_list": []
				}
			}
		}
	},
	"event_handlers": {
		"events": {
			"AuthFailure": [
							{
									"handler_name":"cp_dynamic_handler",
									"handler_meta": {
											"name": "my_handler"
									}
							}
					]
		}
	},
	"custom_middleware": {
		"auth_check": {
			"name": "TestAuthCheck"
		},
		"driver": "python"
	},
	"proxy": {
		"listen_path": "/v1",
		"target_url": "http://httpbin.org",
		"strip_listen_path": false
	}
}`
