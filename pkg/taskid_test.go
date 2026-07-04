// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"github.com/bborbe/github-release-watcher/pkg"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pkg.DeriveTaskID", func() {
	It("DeriveTaskID is deterministic for identical inputs", func() {
		first := pkg.DeriveTaskID(
			"bborbe",
			"docker-utils",
			"d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
		)
		for i := 0; i < 10000; i++ {
			got := pkg.DeriveTaskID(
				"bborbe",
				"docker-utils",
				"d630ef3526cfc57fbdccd9ba53c5c3a02945e407",
			)
			Expect(got).To(Equal(first))
		}
	})

	It("DeriveTaskID differs when owner differs", func() {
		a := pkg.DeriveTaskID("bborbe", "x", "abc")
		b := pkg.DeriveTaskID("other", "x", "abc")
		Expect(a).NotTo(Equal(b))
	})

	It("DeriveTaskID differs when repo differs", func() {
		a := pkg.DeriveTaskID("bborbe", "x", "abc")
		b := pkg.DeriveTaskID("bborbe", "y", "abc")
		Expect(a).NotTo(Equal(b))
	})

	It("DeriveTaskID differs when head_sha differs", func() {
		a := pkg.DeriveTaskID("bborbe", "x", "abc")
		b := pkg.DeriveTaskID("bborbe", "x", "abd")
		Expect(a).NotTo(Equal(b))
	})

	It("DeriveTaskID pins the bborbe/docker-utils d630ef3 namespace contract", func() {
		ns := uuid.MustParse("4f9e2c1a-7b30-4d8f-9a2e-1c5b8d4f3a90")
		expected := uuid.NewSHA1(
			ns,
			[]byte("bborbe/docker-utils@d630ef3526cfc57fbdccd9ba53c5c3a02945e407"),
		)
		Expect(
			pkg.DeriveTaskID("bborbe", "docker-utils", "d630ef3526cfc57fbdccd9ba53c5c3a02945e407"),
		).To(Equal(expected))
	})
})
