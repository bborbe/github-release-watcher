// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg_test

import (
	"context"
	"errors"
	"sync"

	"github.com/bborbe/github-release-watcher/pkg"
	libkv "github.com/bborbe/kv"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("NewMemDB", func() {
	var (
		ctx context.Context
		db  libkv.DB
	)

	BeforeEach(func() {
		ctx = context.Background()
		db = pkg.NewMemDB()
	})

	It("returns a non-nil DB", func() {
		Expect(db).NotTo(BeNil())
	})

	It("implements the libkv.DB interface (Sync/Close/Remove/Stats return nil)", func() {
		// Compile-time interface assertion: NewMemDB() must return a libkv.DB
		// or the result wouldn't bind to `db` above.
		Expect(db).NotTo(BeNil())

		Expect(db.Sync()).To(Succeed())
		_, err := db.Stats(ctx)
		Expect(err).To(BeNil())
		Expect(db.Remove()).To(Succeed())
		// Close last — after Remove, Close is a no-op on the nilled buckets map.
		Expect(db.Close()).To(Succeed())
	})

	It("round-trips a value via Update and View", func() {
		bucketName := libkv.BucketName("offsets")
		key := []byte("k1")
		value := []byte("v1")

		Expect(db.Update(ctx, func(ctx context.Context, tx libkv.Tx) error {
			b, err := tx.CreateBucketIfNotExists(ctx, bucketName)
			if err != nil {
				return err
			}
			return b.Put(ctx, key, value)
		})).To(Succeed())

		var readValue []byte
		Expect(db.View(ctx, func(ctx context.Context, tx libkv.Tx) error {
			b, err := tx.Bucket(ctx, bucketName)
			if err != nil {
				return err
			}
			item, err := b.Get(ctx, key)
			if err != nil {
				return err
			}
			return item.Value(func(val []byte) error {
				readValue = val
				return nil
			})
		})).To(Succeed())

		Expect(readValue).To(Equal(value))
	})

	It("returns BucketNotFoundError when reading a non-existent bucket", func() {
		err := db.View(ctx, func(ctx context.Context, tx libkv.Tx) error {
			_, err := tx.Bucket(ctx, libkv.BucketName("does-not-exist"))
			return err
		})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, libkv.BucketNotFoundError)).To(BeTrue())
	})

	// Race-detector witness for the lock+copy contract on Stats and the
	// RWMutex on Update/View. Under `go test -race` (which `make test` runs
	// per project default), concurrent Update + Stats + View MUST not
	// produce a data-race report. If anyone removes the locks or the
	// value-copy in Stats this test fails the build immediately.
	It("Stats + Update + View are race-free under concurrent access", func() {
		bucketName := libkv.BucketName("offsets")

		// Pre-create the bucket so View has something to read.
		Expect(db.Update(ctx, func(ctx context.Context, tx libkv.Tx) error {
			_, err := tx.CreateBucketIfNotExists(ctx, bucketName)
			return err
		})).To(Succeed())

		var wg sync.WaitGroup
		iters := 50
		wg.Add(3)

		// Writer goroutine: many Update calls (each takes write-lock).
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			for i := 0; i < iters; i++ {
				key := []byte{byte(i)}
				Expect(db.Update(ctx, func(ctx context.Context, tx libkv.Tx) error {
					b, err := tx.Bucket(ctx, bucketName)
					if err != nil {
						return err
					}
					return b.Put(ctx, key, []byte{byte(i)})
				})).To(Succeed())
			}
		}()

		// Reader goroutine: many View calls (each takes read-lock).
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			for i := 0; i < iters; i++ {
				Expect(db.View(ctx, func(ctx context.Context, tx libkv.Tx) error {
					_, err := tx.Bucket(ctx, bucketName)
					return err
				})).To(Succeed())
			}
		}()

		// Stats goroutine: many Stats calls (must lock+copy, not return
		// a pointer to shared state — that's the bug iter-1 fixed).
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			for i := 0; i < iters; i++ {
				_, err := db.Stats(ctx)
				Expect(err).NotTo(HaveOccurred())
			}
		}()

		wg.Wait()
	})
})
