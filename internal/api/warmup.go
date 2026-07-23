package api

import (
	"sync"
)

// warmupPaths 是网页首页 landing 时并发预取的一批无副作用 GET,能触发后端多
// 条下游 gRPC 服务同时唤醒。跨零点第一次访问时 learning/statuses 会返回
// 412(Data Not Ready)长达数分钟——用这批预热请求把 backend 从冷启动状态
// 拽出来。观测:网页版能"秒进"就是靠首页这一批并发预取。
//
// 全部只读、不改进度。任何错误都吞掉——这条链路的价值全在副作用(唤醒后端),
// 返回值本身不用。
//
// /abc/applets/user_applets 是网页学习页 (/#/study/entry) init 的第一步
// (fetchUserApplets),它拉起的是 dashboard API 触达不到的学习页后端链路;
// 排查跨零点 412 久等时发现单靠首页 dashboard 预热唤不醒这条链路,补上它。
var warmupPaths = []string{
	"/abc/applets/user_applets",
	"/wordsapp/user_desk",
	"/wordsapp/user_desk/finished_material_books",
	"/wordsapp/material_book_learning_tasks",
	"/wordsapp/vip/user_item/info",
	"/wordscollection/learning/count?type_of=NEW",
}

// Warmup fires warm-up GETs concurrently and returns immediately. All requests
// run in the background using the client's normal http.Client (30s timeout);
// they finish or die on their own—Warmup does not wait.
//
// Safe to call multiple times: it just fires more probes. In practice call
// once per session, right after CurrentBook, so warm-up runs in parallel with
// LoadContent/LoadQueue on the hot path.
func (c *Client) Warmup() {
	for _, p := range warmupPaths {
		go func() { _, _, _ = c.do("GET", p, nil) }()
	}
}

// warmupOnce 让 retryNotReady 在初始 412 时再补一次 warmup,不重复。
type warmupOnce struct {
	once sync.Once
}

func (w *warmupOnce) fire(c *Client) {
	w.once.Do(c.Warmup)
}
