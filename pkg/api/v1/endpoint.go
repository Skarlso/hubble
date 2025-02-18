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

package v1

import (
	"net"
	"time"
)

// EqualsByID compares if the receiver's endpoint has the same ID, PodName and
// PodNamespace.
func (e *Endpoint) EqualsByID(o *Endpoint) bool {
	return (e.ID == o.ID && e.PodName == "" && e.PodNamespace == "") ||
		e.ID == o.ID &&
			e.PodName == o.PodName &&
			e.PodNamespace == o.PodNamespace
}

// SetFrom sets all fields that are not time based, i.e. Created and Deleted,
// from the given endpoint 'o' in receiver's endpoint.
func (e *Endpoint) SetFrom(o *Endpoint) {
	if o.ContainerIDs != nil {
		e.ContainerIDs = o.ContainerIDs
	}
	if o.ID != 0 {
		e.ID = o.ID
	}
	if o.IPv4 != nil {
		e.IPv4 = o.IPv4
	}
	if o.IPv6 != nil {
		e.IPv6 = o.IPv6
	}
	if o.PodName != "" {
		e.PodName = o.PodName
	}
	if o.PodNamespace != "" {
		e.PodNamespace = o.PodNamespace
	}
}

// SyncEndpoints adds the given list of endpoints in the internal endpoint
// slice. All endpoints in the internal endpoint slice that are not in the given
// 'newEps' slice will be marked as "deleted".
func (es *Endpoints) SyncEndpoints(newEps []*Endpoint) {
	if len(newEps) == 0 {
		return
	}
	es.mutex.Lock()
	defer es.mutex.Unlock()
	// Mark all endpoints not found as deleted
	for _, ep := range es.eps {
		if ep.Deleted != nil {
			continue
		}
		found := false
		for _, updatedEp := range newEps {
			if ep.EqualsByID(updatedEp) {

				found = true
				break
			}
		}
		// If we haven't found it, it means we have lost, or haven't receive
		// yet, an event signalizing that this endpoint was deleted.
		if !found {
			t := time.Now()
			// TODO: remove leftover endpoints if the timestamp of the last
			//  flow written is after the endpoint was deleted.
			//  This requires a method in the ring buffer that returns
			//  the older flow written.
			ep.Deleted = &t
		}
	}

	// Add the endpoint to the list of endpoints.
	for _, updatedEp := range newEps {
		es.updateEndpoint(updatedEp)
	}
}

// FindEPs returns all the EPs that have the given epID or the given namespace
// or the given podName (running in the given namespace).
func (es *Endpoints) FindEPs(epID uint64, namespace string, podName string) []Endpoint {
	var eps []Endpoint
	es.mutex.RLock()
	defer es.mutex.RUnlock()
	for _, ep := range es.eps {
		// If is the endpoint ID we are looking for
		if (epID != 0 && ep.ID == epID) ||
			// The pod name is the one we are looking for
			(podName != "" && (ep.PodName == podName && ep.PodNamespace == namespace)) ||
			// The pod namespace is in the same namespace we are looking for
			(podName == "" && ep.PodNamespace == namespace) {

			eps = append(eps, *ep)
		}
	}

	return eps
}

// updateEndpoint updates the given endpoint if already exists in the slice
// of endpoints. If the endpoint does not exists, it is appended to the slice of
// endpoints.
func (es *Endpoints) updateEndpoint(updateEp *Endpoint) {
	for _, ep := range es.eps {
		if ep.Deleted != nil {
			continue
		}
		// Update endpoint if the ID is the same *and* the podName and
		// podNamespace do not exist, otherwise check if the given updateEp
		// equals to ep.
		if ep.EqualsByID(updateEp) {

			ep.SetFrom(updateEp)

			return
		}
	}
	// If we haven't found it, then we need to add it to the list of
	// endpoints
	es.eps = append(es.eps, updateEp)
}

// UpdateEndpoint updates the given endpoint if already exists in the slice
// of endpoints. If the endpoint does not exists, it is appended to the slice of
// endpoints.
func (es *Endpoints) UpdateEndpoint(updateEp *Endpoint) {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	es.updateEndpoint(updateEp)
}

// MarkDeleted marks the given endpoint as deleted by setting the "Deleted"
// endpoint field with the value of the given 'del' endpoint. If the endpoint is
// not found in the internal slice of endpoints, it's added to the slice of
// endpoints.
func (es *Endpoints) MarkDeleted(del *Endpoint) {
	es.mutex.Lock()
	defer es.mutex.Unlock()
	for _, ep := range es.eps {
		if ep.Deleted != nil {
			continue
		}

		if ep.EqualsByID(del) {
			ep.Deleted = del.Deleted
			return
		}
	}
	es.eps = append(es.eps, del)
}

// GetEndpoint returns the endpoint that has the given ip.
func (es *Endpoints) GetEndpoint(ip net.IP) (endpoint *Endpoint, ok bool) {
	es.mutex.RLock()
	defer es.mutex.RUnlock()
	for _, ep := range es.eps {
		if ep.IPv4.Equal(ip) || ep.IPv6.Equal(ip) {
			return ep, true
		}
	}
	return
}
