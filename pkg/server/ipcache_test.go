// Copyright 2019 Authors of Hubble
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"net"
	"testing"

	"github.com/cilium/cilium/api/v1/models"
	monitorAPI "github.com/cilium/cilium/pkg/monitor/api"
	"github.com/cilium/cilium/pkg/source"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	v1 "github.com/cilium/hubble/pkg/api/v1"
	"github.com/cilium/hubble/pkg/ipcache"
	"github.com/cilium/hubble/pkg/testutils"
)

func TestObserverServer_syncIPCache(t *testing.T) {
	cidr1111 := "1.1.1.1/32"
	cidr2222 := "2.2.2.2/32"
	cidr3333 := "3.3.3.3/32"
	cidr4444 := "4.4.4.4/32"

	ipc := ipcache.New()
	fakeClient := &fakeCiliumClient{
		fakeGetIPCache: func() ([]*models.IPListEntry, error) {
			id100 := int64(100)

			return []*models.IPListEntry{
				{Cidr: &cidr1111, Identity: &id100},
				{Cidr: &cidr2222, Identity: &id100},
				{Cidr: &cidr3333, Identity: &id100, Metadata: &models.IPListEntryMetadata{
					Source:    string(source.Kubernetes),
					Name:      "pod-3",
					Namespace: "ns-3",
				}},
				{Cidr: &cidr4444, Identity: &id100},
			}, nil
		},
	}

	s := &ObserverServer{
		ciliumClient: fakeClient,
		ipcache:      ipc,
		log:          zap.L(),
	}

	ipCacheEvents := make(chan monitorAPI.AgentNotify, 100)
	go func() {
		id100 := uint32(100)
		id200 := uint32(200)

		// stale update, should be ignored
		n, err := monitorAPI.IPCacheNotificationRepr("3.3.3.3/32", id100, &id200, nil, nil, 0, "", "")
		require.NoError(t, err)
		ipCacheEvents <- monitorAPI.AgentNotify{Type: monitorAPI.AgentNotifyIPCacheUpserted, Text: n}

		// delete 2.2.2.2
		n, err = monitorAPI.IPCacheNotificationRepr("2.2.2.2/32", id100, nil, nil, nil, 0, "", "")
		require.NoError(t, err)
		ipCacheEvents <- monitorAPI.AgentNotify{Type: monitorAPI.AgentNotifyIPCacheDeleted, Text: n}

		// reinsert 2.2.2.2 with pod name
		n, err = monitorAPI.IPCacheNotificationRepr("2.2.2.2/32", id100, nil, nil, nil, 0, "ns-2", "pod-2")
		require.NoError(t, err)
		ipCacheEvents <- monitorAPI.AgentNotify{Type: monitorAPI.AgentNotifyIPCacheUpserted, Text: n}

		// update 1.1.1.1 with pod name
		n, err = monitorAPI.IPCacheNotificationRepr("1.1.1.1/32", id100, &id100, nil, nil, 0, "ns-1", "pod-1")
		require.NoError(t, err)
		ipCacheEvents <- monitorAPI.AgentNotify{Type: monitorAPI.AgentNotifyIPCacheUpserted, Text: n}

		// delete 4.4.4.4
		n, err = monitorAPI.IPCacheNotificationRepr("4.4.4.4/32", id100, nil, nil, nil, 0, "", "")
		require.NoError(t, err)
		ipCacheEvents <- monitorAPI.AgentNotify{Type: monitorAPI.AgentNotifyIPCacheDeleted, Text: n}

		close(ipCacheEvents)
	}()

	// blocks until channel is closed
	s.syncIPCache(ipCacheEvents)
	assert.Equal(t, 0, len(ipCacheEvents))

	tests := []struct {
		ip  net.IP
		ns  string
		pod string
		ok  bool
	}{
		{ip: net.ParseIP("1.1.1.1"), ns: "ns-1", pod: "pod-1", ok: true},
		{ip: net.ParseIP("2.2.2.2"), ns: "ns-2", pod: "pod-2", ok: true},
		{ip: net.ParseIP("3.3.3.3"), ns: "ns-3", pod: "pod-3", ok: true},
		{ip: net.ParseIP("4.4.4.4"), ok: false},
	}
	for _, tt := range tests {
		gotNs, gotPod, gotOk := ipc.GetPodNameOf(tt.ip)
		if gotNs != tt.ns {
			t.Errorf("IPCache.GetPodNameOf() gotNs = %v, want %v", gotNs, tt.ns)
		}
		if gotPod != tt.pod {
			t.Errorf("IPCache.GetPodNameOf() gotPod = %v, want %v", gotPod, tt.pod)
		}
		if gotOk != tt.ok {
			t.Errorf("IPCache.GetPodNameOf() gotOk = %v, want %v", gotOk, tt.ok)
		}
	}
}

func TestLegacyPodGetter_GetPodNameOf(t *testing.T) {
	type fields struct {
		PodGetter      podGetter
		EndpointGetter endpointsHandler
	}
	type args struct {
		ip net.IP
	}
	type want struct {
		ns  string
		pod string
		ok  bool
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			name: "available in ipcache",
			fields: fields{
				PodGetter: &testutils.FakeK8sGetter{
					OnGetPodNameOf: func(_ net.IP) (ns, pod string, ok bool) {
						return "default", "xwing", true
					},
				},
				EndpointGetter: &fakeEndpointsHandler{
					fakeGetEndpoint: func(_ net.IP) (*v1.Endpoint, bool) {
						return nil, false
					},
				},
			},
			args: args{
				ip: net.ParseIP("1.1.1.15"),
			},
			want: want{
				ns:  "default",
				pod: "xwing",
				ok:  true,
			},
		},
		{
			name: "available in endpoints",
			fields: fields{
				PodGetter: &testutils.FakeK8sGetter{
					OnGetPodNameOf: func(_ net.IP) (ns, pod string, ok bool) {
						return "", "", false
					},
				},
				EndpointGetter: &fakeEndpointsHandler{
					fakeGetEndpoint: func(_ net.IP) (*v1.Endpoint, bool) {
						return &v1.Endpoint{
							ID:           16,
							IPv4:         net.ParseIP("1.1.1.15"),
							PodName:      "deathstar",
							PodNamespace: "default",
						}, true
					},
				},
			},
			args: args{
				ip: net.ParseIP("1.1.1.15"),
			},
			want: want{
				ns:  "default",
				pod: "deathstar",
				ok:  true,
			},
		},
		{
			name: "available in both",
			fields: fields{
				PodGetter: &testutils.FakeK8sGetter{
					OnGetPodNameOf: func(_ net.IP) (ns, pod string, ok bool) {
						return "default", "xwing", true
					},
				},
				EndpointGetter: &fakeEndpointsHandler{
					fakeGetEndpoint: func(_ net.IP) (*v1.Endpoint, bool) {
						return &v1.Endpoint{
							ID:           16,
							IPv4:         net.ParseIP("1.1.1.15"),
							PodName:      "deathstar",
							PodNamespace: "default",
						}, true
					},
				},
			},
			args: args{
				ip: net.ParseIP("1.1.1.15"),
			},
			want: want{
				ns:  "default",
				pod: "xwing",
				ok:  true,
			},
		},
		{
			name: "available in none",
			fields: fields{
				PodGetter: &testutils.FakeK8sGetter{
					OnGetPodNameOf: func(_ net.IP) (ns, pod string, ok bool) {
						return "", "", false
					},
				},
				EndpointGetter: &fakeEndpointsHandler{
					fakeGetEndpoint: func(_ net.IP) (*v1.Endpoint, bool) {
						return nil, false
					},
				},
			},
			args: args{
				ip: net.ParseIP("1.1.1.15"),
			},
			want: want{
				ok: false,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := &LegacyPodGetter{
				PodGetter:      tt.fields.PodGetter,
				EndpointGetter: tt.fields.EndpointGetter,
			}
			gotNs, gotPod, gotOk := l.GetPodNameOf(tt.args.ip)
			if gotNs != tt.want.ns {
				t.Errorf("LegacyPodGetter.GetPodNameOf() gotNs = %v, want %v", gotNs, tt.want.ns)
			}
			if gotPod != tt.want.pod {
				t.Errorf("LegacyPodGetter.GetPodNameOf() gotPod = %v, want %v", gotPod, tt.want.pod)
			}
			if gotOk != tt.want.ok {
				t.Errorf("LegacyPodGetter.GetPodNameOf() gotOk = %v, want %v", gotOk, tt.want.ok)
			}
		})
	}
}
