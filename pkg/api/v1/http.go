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
	pb "github.com/cilium/hubble/api/v1/observer"
)

// LooseCompareHTTP returns true if both HTTP flows are loosely identical. This
// means that the following fields must match:
//  - Code
//  - Method
//  - Url
//  - Protocol
func LooseCompareHTTP(a, b *pb.HTTP) bool {
	return a.Code == b.Code && a.Method == b.Method && a.Url == b.Url && a.Protocol == b.Protocol
}
