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

package threefour

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/byteorder"
	"github.com/cilium/cilium/pkg/identity"
	"github.com/cilium/cilium/pkg/monitor"
	"github.com/cilium/cilium/pkg/monitor/api"
	"github.com/cilium/hubble/api/v1/observer"
	pb "github.com/cilium/hubble/api/v1/observer"
	v1 "github.com/cilium/hubble/pkg/api/v1"
	"github.com/cilium/hubble/pkg/testutils"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"

	"github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeIdentityGetter struct {
	OnGetIdentity func(securityIdentity uint64) (*models.Identity, error)
}

func (f *fakeIdentityGetter) GetIdentity(securityIdentity uint64) (*models.Identity, error) {
	if f.OnGetIdentity != nil {
		return f.OnGetIdentity(securityIdentity)
	}
	panic("OnGetIdentity not set")
}

func TestL34Decode(t *testing.T) {
	//SOURCE          					DESTINATION           TYPE   SUMMARY
	//192.168.33.11:6443(sun-sr-https)  10.16.236.178:54222   L3/4   TCP Flags: ACK
	d := []byte{
		4, 7, 0, 0, 7, 124, 26, 57, 66, 0, 0, 0, 66, 0, 0, 0, 1, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 246, 141, 178, 45, 33, 217, 246, 141, 178,
		45, 33, 217, 8, 0, 69, 0, 0, 52, 234, 28, 64, 0, 64, 6, 120, 49, 192,
		168, 33, 11, 10, 16, 236, 178, 25, 43, 211, 206, 42, 239, 210, 28, 180,
		152, 129, 103, 128, 16, 1, 152, 216, 156, 0, 0, 1, 1, 8, 10, 0, 90, 176,
		98, 0, 90, 176, 97, 0, 0}

	endpointGetter := &testutils.FakeEndpointGetter{
		OnGetEndpoint: func(ip net.IP) (endpoint *v1.Endpoint, ok bool) {
			if ip.Equal(net.ParseIP("10.16.236.178")) {
				return &v1.Endpoint{
					ID:           1234,
					PodName:      "pod-10.16.236.178",
					PodNamespace: "default",
				}, true
			}
			return nil, false
		},
	}
	dnsGetter := &testutils.FakeDNSGetter{
		OnGetNamesOf: func(epID uint64, ip net.IP) (names []string) {
			if epID == 1234 {
				switch {
				case ip.Equal(net.ParseIP("192.168.33.11")):
					return []string{"host-192.168.33.11"}
				}
			}
			return nil
		},
	}
	k8sGetter := &testutils.FakeK8sGetter{
		OnGetPodNameOf: func(ip net.IP) (ns, pod string, ok bool) {
			if ip.Equal(net.ParseIP("10.16.236.178")) {
				return "default", "pod-10.16.236.178", true
			}
			return
		},
	}
	identityCache := &fakeIdentityGetter{
		OnGetIdentity: func(securityIdentity uint64) (*models.Identity, error) {
			return &models.Identity{}, nil
		},
	}
	timestamp := &types.Timestamp{
		Seconds: 1234,
		Nanos:   4884,
	}
	nodeName := "k8s1"
	parser, err := New(endpointGetter, identityCache, dnsGetter, k8sGetter)
	require.NoError(t, err)

	f := &pb.Flow{}
	p := &pb.Payload{
		Type:     pb.EventType_EventSample,
		Time:     timestamp,
		Data:     d,
		HostName: nodeName,
	}
	err = parser.Decode(p, f)
	require.NoError(t, err)

	assert.Equal(t, []string{"host-192.168.33.11"}, f.GetSourceNames())
	assert.Equal(t, "192.168.33.11", f.GetIP().GetSource())
	assert.Equal(t, uint32(6443), f.L4.GetTCP().GetSourcePort())
	assert.Equal(t, "", f.GetSource().GetPodName())
	assert.Equal(t, "", f.GetSource().GetNamespace())

	assert.Equal(t, []string(nil), f.GetDestinationNames())
	assert.Equal(t, "10.16.236.178", f.GetIP().GetDestination())
	assert.Equal(t, uint32(54222), f.L4.GetTCP().GetDestinationPort())
	assert.Equal(t, "pod-10.16.236.178", f.GetDestination().GetPodName())
	assert.Equal(t, "default", f.GetDestination().GetNamespace())

	assert.Equal(t, int32(api.MessageTypeTrace), f.GetEventType().GetType())
	assert.Equal(t, int32(api.TraceFromHost), f.GetEventType().GetSubType())
	assert.Equal(t, pb.Verdict_FORWARDED, f.GetVerdict())
	assert.Equal(t, &pb.TCPFlags{ACK: true}, f.L4.GetTCP().GetFlags())
	assert.Equal(t, nodeName, f.GetNodeName())
	assert.Equal(t, p, f.GetPayload())

	// ICMP packet so no ports until that support is merged into master
	//
	//SOURCE              DESTINATION          TYPE   SUMMARY
	//ff02::1:ff00:b3e5   f00d::a10:0:0:9195   L3/4
	d2 := []byte{
		4, 5, 168, 11, 95, 22, 242, 184, 86, 0, 0, 0, 86, 0, 0, 0, 104, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 51, 51, 255, 0, 179, 229, 18, 145,
		6, 226, 34, 26, 134, 221, 96, 0, 0, 0, 0, 32, 58, 255, 255, 2, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 1, 255, 0, 179, 229, 240, 13, 0, 0, 0, 0, 0, 0, 10,
		16, 0, 0, 0, 0, 145, 149, 135, 0, 80, 117, 0, 0, 0, 0, 240, 13, 0, 0, 0,
		0, 0, 0, 10, 16, 0, 0, 0, 0, 179, 229, 1, 1, 18, 145, 6, 226, 34, 26, 0,
		0, 0, 0, 0, 0}

	endpointGetter = &testutils.FakeEndpointGetter{
		OnGetEndpoint: func(ip net.IP) (endpoint *v1.Endpoint, ok bool) {
			if ip.Equal(net.ParseIP("ff02::1:ff00:b3e5")) {
				return &v1.Endpoint{
					ID: 1234,
				}, true
			}
			return nil, false
		},
	}
	dnsGetter = &testutils.FakeDNSGetter{
		OnGetNamesOf: func(epID uint64, ip net.IP) (names []string) {
			if epID == 1234 {
				switch {
				case ip.Equal(net.ParseIP("f00d::a10:0:0:9195")):
					return []string{"host-f00d::a10:0:0:9195"}
				}
			}
			return nil
		},
	}
	k8sGetter = &testutils.FakeK8sGetter{
		OnGetPodNameOf: func(ip net.IP) (ns, pod string, ok bool) {
			return
		},
	}
	parser, err = New(endpointGetter, identityCache, dnsGetter, k8sGetter)
	require.NoError(t, err)

	p = &pb.Payload{
		Type:     pb.EventType_EventSample,
		Time:     timestamp,
		Data:     d2,
		HostName: nodeName,
	}
	err = parser.Decode(p, f)
	require.NoError(t, err)

	// second packet is ICMPv6 and the flags should be totally wiped out
	assert.Equal(t, []string(nil), f.GetSourceNames())
	assert.Equal(t, "ff02::1:ff00:b3e5", f.GetIP().GetSource())
	assert.Equal(t, &pb.ICMPv6{Type: 135}, f.L4.GetICMPv6())
	assert.Equal(t, "", f.GetSource().GetPodName())
	assert.Equal(t, "", f.GetSource().GetNamespace())

	assert.Equal(t, []string{"host-f00d::a10:0:0:9195"}, f.GetDestinationNames())
	assert.Equal(t, "f00d::a10:0:0:9195", f.GetIP().GetDestination())
	assert.Equal(t, "", f.GetDestination().GetPodName())
	assert.Equal(t, "", f.GetDestination().GetNamespace())

	assert.Equal(t, int32(api.MessageTypeTrace), f.GetEventType().GetType())
	assert.Equal(t, int32(api.TraceFromLxc), f.GetEventType().GetSubType())
	assert.Equal(t, pb.Verdict_FORWARDED, f.GetVerdict())
	assert.Equal(t, (*observer.TCPFlags)(nil), f.L4.GetTCP().GetFlags())
	assert.Equal(t, nodeName, f.GetNodeName())
	assert.Equal(t, p, f.GetPayload())
}

func BenchmarkL34Decode(b *testing.B) {
	d := []byte{4, 7, 0, 0, 7, 124, 26, 57, 66, 0, 0, 0, 66, 0, 0, 0, 1, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 246, 141, 178, 45, 33, 217, 246, 141,
		178, 45, 33, 217, 8, 0, 69, 0, 0, 52, 234, 28, 64, 0, 64, 6, 120, 49, 192,
		168, 33, 11, 10, 16, 236, 178, 25, 43, 211, 206, 42, 239, 210, 28, 180, 152,
		129, 103, 128, 16, 1, 152, 216, 156, 0, 0, 1, 1, 8, 10, 0, 90, 176, 98, 0,
		90, 176, 97, 0, 0}

	endpointGetter := &testutils.FakeEndpointGetter{
		OnGetEndpoint: func(ip net.IP) (*v1.Endpoint, bool) {
			return nil, false
		},
	}
	dnsGetter := &testutils.FakeDNSGetter{
		OnGetNamesOf: func(epID uint64, ip net.IP) (names []string) {
			return nil
		},
	}
	k8sGetter := &testutils.FakeK8sGetter{
		OnGetPodNameOf: func(ip net.IP) (ns, pod string, ok bool) {
			return
		},
	}
	identityCache := &fakeIdentityGetter{OnGetIdentity: func(securityIdentity uint64) (*models.Identity, error) {
		return &models.Identity{}, nil
	}}
	timestamp := &types.Timestamp{
		Seconds: 1234,
		Nanos:   4884,
	}
	nodeName := "k8s1"
	parser, err := New(endpointGetter, identityCache, dnsGetter, k8sGetter)
	require.NoError(b, err)

	f := &pb.Flow{}
	p := &pb.Payload{
		Type:     pb.EventType_EventSample,
		Time:     timestamp,
		Data:     d,
		HostName: nodeName,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.Decode(p, f)
	}
}

func TestDecodeTraceNotify(t *testing.T) {
	buf := &bytes.Buffer{}
	tn := monitor.TraceNotifyV0{
		Type:     byte(api.MessageTypeTrace),
		SrcLabel: 123,
		DstLabel: 456,
	}
	err := binary.Write(buf, byteorder.Native, &tn)
	require.NoError(t, err)
	buffer := gopacket.NewSerializeBuffer()
	err = gopacket.SerializeLayers(buffer,
		gopacket.SerializeOptions{},
		&layers.Ethernet{
			SrcMAC: net.HardwareAddr{1, 2, 3, 4, 5, 6},
			DstMAC: net.HardwareAddr{1, 2, 3, 4, 5, 6},
		},
		&layers.IPv4{
			SrcIP: net.IPv4(1, 2, 3, 4),
			DstIP: net.IPv4(1, 2, 3, 4),
		},
	)
	require.NoError(t, err)
	buf.Write(buffer.Bytes())
	require.NoError(t, err)
	identityGetter := &fakeIdentityGetter{OnGetIdentity: func(securityIdentity uint64) (*models.Identity, error) {
		if securityIdentity == (uint64)(tn.SrcLabel) {
			return &models.Identity{Labels: []string{"src=label"}}, nil
		} else if securityIdentity == (uint64)(tn.DstLabel) {
			return &models.Identity{Labels: []string{"dst=label"}}, nil
		}
		return nil, fmt.Errorf("identity not found for %d", securityIdentity)
	}}

	parser, err := New(&testutils.NoopEndpointGetter, identityGetter, &testutils.NoopDNSGetter, &testutils.NoopK8sGetter)
	require.NoError(t, err)

	f := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: buf.Bytes()}, f)
	require.NoError(t, err)
	assert.Equal(t, []string{"src=label"}, f.GetSource().GetLabels())
	assert.Equal(t, []string{"dst=label"}, f.GetDestination().GetLabels())
}

func TestDecodeDropNotify(t *testing.T) {
	buf := &bytes.Buffer{}
	dn := monitor.DropNotify{
		Type:     byte(api.MessageTypeDrop),
		SrcLabel: 123,
		DstLabel: 456,
	}
	err := binary.Write(buf, byteorder.Native, &dn)
	require.NoError(t, err)
	buffer := gopacket.NewSerializeBuffer()
	err = gopacket.SerializeLayers(buffer,
		gopacket.SerializeOptions{},
		&layers.Ethernet{
			SrcMAC: net.HardwareAddr{1, 2, 3, 4, 5, 6},
			DstMAC: net.HardwareAddr{1, 2, 3, 4, 5, 6},
		},
		&layers.IPv4{
			SrcIP: net.IPv4(1, 2, 3, 4),
			DstIP: net.IPv4(1, 2, 3, 4),
		},
	)
	require.NoError(t, err)
	buf.Write(buffer.Bytes())
	require.NoError(t, err)
	identityGetter := &fakeIdentityGetter{OnGetIdentity: func(securityIdentity uint64) (*models.Identity, error) {
		if securityIdentity == (uint64)(dn.SrcLabel) {
			return &models.Identity{Labels: []string{"src=label"}}, nil
		} else if securityIdentity == (uint64)(dn.DstLabel) {
			return &models.Identity{Labels: []string{"dst=label"}}, nil
		}
		return nil, fmt.Errorf("identity not found for %d", securityIdentity)
	}}

	parser, err := New(&testutils.NoopEndpointGetter, identityGetter, &testutils.NoopDNSGetter, &testutils.NoopK8sGetter)
	require.NoError(t, err)

	f := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: buf.Bytes()}, f)
	require.NoError(t, err)
	assert.Equal(t, []string{"src=label"}, f.GetSource().GetLabels())
	assert.Equal(t, []string{"dst=label"}, f.GetDestination().GetLabels())
}

func TestDecodeDropReason(t *testing.T) {
	reason := uint8(130)
	dn := monitor.DropNotify{
		Type:    byte(api.MessageTypeDrop),
		SubType: reason,
	}
	data, err := testutils.CreateL3L4Payload(dn)
	require.NoError(t, err)

	parser, err := New(nil, nil, nil, nil)
	require.NoError(t, err)

	f := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: data}, f)
	require.NoError(t, err)

	assert.Equal(t, uint32(reason), f.GetDropReason())
}

func TestDecodeLocalIdentity(t *testing.T) {
	tn := monitor.TraceNotifyV0{
		Type:     byte(api.MessageTypeTrace),
		SrcLabel: uint32(123 | identity.LocalIdentityFlag),
		DstLabel: uint32(456 | identity.LocalIdentityFlag),
	}
	data, err := testutils.CreateL3L4Payload(tn)
	require.NoError(t, err)
	identityGetter := &fakeIdentityGetter{OnGetIdentity: func(securityIdentity uint64) (*models.Identity, error) {
		return &models.Identity{Labels: []string{"some=label", "cidr:1.2.3.4/12", "cidr:1.2.3.4/11"}}, nil
	}}

	parser, err := New(nil, identityGetter, nil, nil)
	require.NoError(t, err)

	f := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: data}, f)
	require.NoError(t, err)

	assert.Equal(t, []string{"cidr:1.2.3.4/12", "some=label"}, f.GetSource().GetLabels())
	assert.Equal(t, []string{"cidr:1.2.3.4/12", "some=label"}, f.GetDestination().GetLabels())
}

func Test_filterCidrLabels(t *testing.T) {
	type args struct {
		labels []string
	}
	tests := []struct {
		name string
		args args
		want []string
	}{
		{
			name: "mixed",
			args: args{
				labels: []string{"b", "cidr:1.1.1.1/23", "a", "d", "cidr:1.1.1.1/24"},
			},
			want: []string{"b", "a", "d", "cidr:1.1.1.1/24"},
		},
		{
			name: "no-cidr",
			args: args{
				labels: []string{"b", "c", "a"},
			},
			want: []string{"b", "c", "a"},
		},
		{
			name: "cidr-only",
			args: args{
				labels: []string{"cidr:1.1.1.1/0", "cidr:1.1.1.1/32", "cidr:1.1.1.1/16"},
			},
			want: []string{"cidr:1.1.1.1/32"},
		},
		{
			name: "empty",
			args: args{
				labels: []string{},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterCidrLabels(tt.args.labels); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterCidrLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTraceNotifyOriginalIP(t *testing.T) {
	f := &pb.Flow{}
	parser, err := New(&testutils.NoopEndpointGetter, nil, &testutils.NoopDNSGetter, &testutils.NoopK8sGetter)
	require.NoError(t, err)

	v0 := monitor.TraceNotifyV0{
		Type:    byte(api.MessageTypeTrace),
		Version: monitor.TraceNotifyVersion0,
	}
	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
		DstMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
	}
	ip := layers.IPv4{
		SrcIP: net.ParseIP("2.2.2.2"),
		DstIP: net.ParseIP("3.3.3.3"),
	}
	data, err := testutils.CreateL3L4Payload(v0, &eth, &ip, &layers.TCP{})
	require.NoError(t, err)

	err = parser.Decode(&pb.Payload{Data: data}, f)
	require.NoError(t, err)
	assert.Equal(t, f.IP.Source, "2.2.2.2")

	v1 := monitor.TraceNotifyV1{
		TraceNotifyV0: monitor.TraceNotifyV0{
			Type:    byte(api.MessageTypeTrace),
			Version: monitor.TraceNotifyVersion1,
		},
		OrigIP: [16]byte{1, 1, 1, 1},
	}
	data, err = testutils.CreateL3L4Payload(v1, &eth, &ip, &layers.TCP{})
	err = parser.Decode(&pb.Payload{Data: data}, f)
	require.NoError(t, err)
	assert.Equal(t, f.IP.Source, "1.1.1.1")
}

func TestICMP(t *testing.T) {
	parser, err := New(&testutils.NoopEndpointGetter, nil, &testutils.NoopDNSGetter, &testutils.NoopK8sGetter)
	require.NoError(t, err)
	message := monitor.TraceNotifyV1{
		TraceNotifyV0: monitor.TraceNotifyV0{
			Type:    byte(api.MessageTypeTrace),
			Version: monitor.TraceNotifyVersion1,
		},
	}

	// icmpv4
	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
		DstMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
	}
	ip := layers.IPv4{
		SrcIP:    net.ParseIP("2.2.2.2"),
		DstIP:    net.ParseIP("3.3.3.3"),
		Protocol: layers.IPProtocolICMPv4,
	}
	icmpv4 := layers.ICMPv4{
		TypeCode: layers.CreateICMPv4TypeCode(1, 2),
	}
	v4data, err := testutils.CreateL3L4Payload(message, &eth, &ip, &icmpv4)
	require.NoError(t, err)
	v4flow := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: v4data}, v4flow)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), v4flow.GetL4().GetICMPv4().Type)
	assert.Equal(t, uint32(2), v4flow.GetL4().GetICMPv4().Code)

	// icmpv4
	ethv6 := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv6,
		SrcMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
		DstMAC:       net.HardwareAddr{2, 3, 4, 5, 6, 7},
	}
	ipv6 := layers.IPv6{
		Version:    0x6,
		NextHeader: 0x3a,
		SrcIP:      net.ParseIP("::"),
		DstIP:      net.ParseIP("::"),
	}
	icmpv6 := layers.ICMPv6{
		TypeCode: layers.CreateICMPv6TypeCode(3, 4),
	}
	v6data, err := testutils.CreateL3L4Payload(message, &ethv6, &ipv6, &icmpv6)
	require.NoError(t, err)
	v6flow := &pb.Flow{}
	err = parser.Decode(&pb.Payload{Data: v6data}, v6flow)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), v6flow.GetL4().GetICMPv6().Type)
	assert.Equal(t, uint32(4), v6flow.GetL4().GetICMPv6().Code)
}

func TestTraceNotifyLocalEndpoint(t *testing.T) {
	f := &pb.Flow{}

	ep := &v1.Endpoint{
		ID:           1234,
		IPv4:         net.ParseIP("1.1.1.1"),
		PodName:      "xwing",
		PodNamespace: "default",
		Labels:       []string{"a", "b", "c"},
	}
	endpointGetter := &testutils.FakeEndpointGetter{
		OnGetEndpoint: func(ip net.IP) (endpoint *v1.Endpoint, ok bool) {
			return ep, true
		},
	}

	parser, err := New(endpointGetter, nil, &testutils.NoopDNSGetter, &testutils.NoopK8sGetter)
	require.NoError(t, err)

	v0 := monitor.TraceNotifyV0{
		Type:     byte(api.MessageTypeTrace),
		SrcLabel: 456,
		Version:  monitor.TraceNotifyVersion0,
	}

	eth := layers.Ethernet{
		EthernetType: layers.EthernetTypeIPv4,
		SrcMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
		DstMAC:       net.HardwareAddr{1, 2, 3, 4, 5, 6},
	}
	ip := layers.IPv4{
		SrcIP:    net.ParseIP("2.2.2.2"),
		DstIP:    net.ParseIP("3.3.3.3"),
		Protocol: layers.IPProtocolTCP,
	}
	data, err := testutils.CreateL3L4Payload(v0, &eth, &ip, &layers.TCP{})
	require.NoError(t, err)

	err = parser.Decode(&pb.Payload{Data: data}, f)
	require.NoError(t, err)

	assert.Equal(t, ep.ID, f.Source.ID)
	assert.Equal(t, uint64(v0.SrcLabel), f.Source.Identity)
	assert.Equal(t, ep.PodNamespace, f.Source.Namespace)
	assert.Equal(t, ep.Labels, f.Source.Labels)
	assert.Equal(t, ep.PodName, f.Source.PodName)
}
