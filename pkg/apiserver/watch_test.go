/*
Copyright 2014 Google Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package apiserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"code.google.com/p/go.net/websocket"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
)

var watchTestTable = []struct {
	t   watch.EventType
	obj runtime.Object
}{
	{watch.Added, &Simple{Name: "A Name"}},
	{watch.Modified, &Simple{Name: "Another Name"}},
	{watch.Deleted, &Simple{Name: "Another Name"}},
}

func TestWatchWebsocket(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	_ = ResourceWatcher(simpleStorage) // Give compile error if this doesn't work.
	handler := Handle(map[string]RESTStorage{
		"foo": simpleStorage,
	}, codec, "/prefix/version")
	server := httptest.NewServer(handler)

	dest, _ := url.Parse(server.URL)
	dest.Scheme = "ws" // Required by websocket, though the server never sees it.
	dest.Path = "/prefix/version/watch/foo"
	dest.RawQuery = ""

	ws, err := websocket.Dial(dest.String(), "", "http://localhost")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	try := func(action watch.EventType, object runtime.Object) {
		// Send
		simpleStorage.fakeWatch.Action(action, object)
		// Test receive
		var got api.WatchEvent
		err := websocket.JSON.Receive(ws, &got)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if got.Type != action {
			t.Errorf("Unexpected type: %v", got.Type)
		}
		if e, a := object, got.Object.Object; !reflect.DeepEqual(e, a) {
			t.Errorf("Expected %v, got %v", e, a)
		}
	}

	for _, item := range watchTestTable {
		try(item.t, item.obj)
	}
	simpleStorage.fakeWatch.Stop()

	var got api.WatchEvent
	err = websocket.JSON.Receive(ws, &got)
	if err == nil {
		t.Errorf("Unexpected non-error")
	}
}

func TestWatchHTTP(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := Handle(map[string]RESTStorage{
		"foo": simpleStorage,
	}, codec, "/prefix/version")
	server := httptest.NewServer(handler)
	client := http.Client{}

	dest, _ := url.Parse(server.URL)
	dest.Path = "/prefix/version/watch/foo"
	dest.RawQuery = ""

	request, err := http.NewRequest("GET", dest.String(), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if response.StatusCode != http.StatusOK {
		t.Errorf("Unexpected response %#v", response)
	}

	decoder := json.NewDecoder(response.Body)

	for i, item := range watchTestTable {
		// Send
		simpleStorage.fakeWatch.Action(item.t, item.obj)
		// Test receive
		var got api.WatchEvent
		err := decoder.Decode(&got)
		if err != nil {
			t.Fatalf("%d: Unexpected error: %v", i, err)
		}
		if got.Type != item.t {
			t.Errorf("%d: Unexpected type: %v", i, got.Type)
		}
		if e, a := item.obj, got.Object.Object; !reflect.DeepEqual(e, a) {
			t.Errorf("%d: Expected %v, got %v", i, e, a)
		}
	}
	simpleStorage.fakeWatch.Stop()

	var got api.WatchEvent
	err = decoder.Decode(&got)
	if err == nil {
		t.Errorf("Unexpected non-error")
	}
}

func TestWatchParamParsing(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := Handle(map[string]RESTStorage{
		"foo": simpleStorage,
	}, codec, "/prefix/version")
	server := httptest.NewServer(handler)

	dest, _ := url.Parse(server.URL)
	dest.Path = "/prefix/version/watch/foo"

	table := []struct {
		rawQuery        string
		resourceVersion uint64
		labelSelector   string
		fieldSelector   string
	}{
		{
			rawQuery:        "resourceVersion=1234",
			resourceVersion: 1234,
			labelSelector:   "",
			fieldSelector:   "",
		}, {
			rawQuery:        "resourceVersion=314159&fields=Host%3D&labels=name%3Dfoo",
			resourceVersion: 314159,
			labelSelector:   "name=foo",
			fieldSelector:   "Host=",
		}, {
			rawQuery:        "fields=ID%3dfoo&resourceVersion=1492",
			resourceVersion: 1492,
			labelSelector:   "",
			fieldSelector:   "ID=foo",
		}, {
			rawQuery:        "",
			resourceVersion: 0,
			labelSelector:   "",
			fieldSelector:   "",
		},
	}

	for _, item := range table {
		simpleStorage.requestedLabelSelector = nil
		simpleStorage.requestedFieldSelector = nil
		simpleStorage.requestedResourceVersion = 5 // Prove this is set in all cases
		dest.RawQuery = item.rawQuery
		resp, err := http.Get(dest.String())
		if err != nil {
			t.Errorf("%v: unexpected error: %v", item.rawQuery, err)
			continue
		}
		resp.Body.Close()
		if e, a := item.resourceVersion, simpleStorage.requestedResourceVersion; e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
		if e, a := item.labelSelector, simpleStorage.requestedLabelSelector.String(); e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
		if e, a := item.fieldSelector, simpleStorage.requestedFieldSelector.String(); e != a {
			t.Errorf("%v: expected %v, got %v", item.rawQuery, e, a)
		}
	}
}

func TestWatchProtocolSelection(t *testing.T) {
	simpleStorage := &SimpleRESTStorage{}
	handler := Handle(map[string]RESTStorage{
		"foo": simpleStorage,
	}, codec, "/prefix/version")
	server := httptest.NewServer(handler)
	client := http.Client{}

	dest, _ := url.Parse(server.URL)
	dest.Path = "/prefix/version/watch/foo"
	dest.RawQuery = ""

	table := []struct {
		isWebsocket bool
		connHeader  string
	}{
		{true, "Upgrade"},
		{true, "keep-alive, Upgrade"},
		{true, "upgrade"},
		{false, "keep-alive"},
	}

	for _, item := range table {
		request, err := http.NewRequest("GET", dest.String(), nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		request.Header.Set("Connection", item.connHeader)
		request.Header.Set("Upgrade", "websocket")

		response, err := client.Do(request)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// The requests recognized as websocket requests based on connection
		// and upgrade headers will not also have the necessary Sec-Websocket-*
		// headers so it is expected to throw a 400
		if item.isWebsocket && response.StatusCode != http.StatusBadRequest {
			t.Errorf("Unexpected response %#v", response)
		}

		if !item.isWebsocket && response.StatusCode != http.StatusOK {
			t.Errorf("Unexpected response %#v", response)
		}
	}
}
