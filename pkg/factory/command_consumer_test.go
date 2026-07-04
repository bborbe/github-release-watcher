// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package factory_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"

	"github.com/bborbe/cqrs/base"
	"github.com/bborbe/github-release-watcher/mocks"
	"github.com/bborbe/github-release-watcher/pkg/factory"
	libkafkamocks "github.com/bborbe/kafka/mocks"
	kvmocks "github.com/bborbe/kv/mocks"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CreateTriggerReleaseCheckCommandSender", func() {
	It("returns a non-nil sender", func() {
		syncProducer := new(libkafkamocks.KafkaSyncProducer)
		sender := factory.CreateTriggerReleaseCheckCommandSender(
			context.Background(),
			syncProducer,
			base.TopicPrefix("develop"),
		)
		Expect(sender).NotTo(BeNil())
	})
})

var _ = Describe("CreateCommandConsumer", func() {
	It("returns a non-nil run.Func when all dependencies are non-nil", func() {
		syncProducer := new(libkafkamocks.KafkaSyncProducer)
		saramaClientProvider := new(libkafkamocks.KafkaSaramaClientProvider)
		db := new(kvmocks.DB)
		watcher := new(mocks.Watcher)

		runFunc := factory.CreateCommandConsumer(
			saramaClientProvider,
			syncProducer,
			db,
			watcher,
			base.TopicPrefix("develop"),
		)
		Expect(runFunc).NotTo(BeNil())
	})

	It("CreateCommandConsumer body has no control flow", func() {
		// Resolve factory.go relative to THIS test file so the test runs
		// correctly regardless of CWD (e.g. when go test is invoked from
		// the module root with ./... rather than from the package dir).
		_, thisFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "runtime.Caller failed")
		factoryPath := filepath.Join(filepath.Dir(thisFile), "factory.go")

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, factoryPath, nil, parser.AllErrors)
		Expect(err).NotTo(HaveOccurred())
		var fn *ast.FuncDecl
		for _, decl := range file.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok && f.Name.Name == "CreateCommandConsumer" {
				fn = f
				break
			}
		}
		Expect(fn).NotTo(BeNil(), "CreateCommandConsumer not found")
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
				Fail(fmt.Sprintf(
					"CreateCommandConsumer body contains forbidden control flow: %T at %v",
					n, fset.Position(n.Pos()),
				))
			}
			return true
		})
	})
})
