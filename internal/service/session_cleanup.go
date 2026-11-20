package service

import (
	"log"
	"time"

	"github.com/nowen-reader/nowen-reader/internal/store"
)

// StartSessionCleanup 定期清理过期的用户 Session（每 6 小时执行一次）。
// 孤儿内容缓存 GC 在首个周期执行，不在重启后立即遍历缓存。
func StartSessionCleanup() {
	go func() {
		cleanExpiredSessions()

		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()

		for range ticker.C {
			cleanExpiredSessions()
		}
	}()

	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			cleanupOrphanContentCaches()
		}
	}()

	log.Println("[session-cleanup] Session cleanup scheduler started (interval: 6h)")
	log.Println("[cache-gc] Orphan content cache cleanup scheduled (interval: 30m)")
}

func cleanExpiredSessions() {
	count, err := store.CleanExpiredSessions()
	if err != nil {
		log.Printf("[session-cleanup] Error cleaning sessions: %v", err)
		return
	}
	if count > 0 {
		log.Printf("[session-cleanup] Cleaned %d expired sessions", count)
	}
}
