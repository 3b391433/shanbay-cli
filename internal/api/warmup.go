package api

import (
	"net/http"
	"sync"
)

// warmupPaths 是 apiv3 首页 landing 时并发预取的一批无副作用 GET,能触发后端
// 多条下游 gRPC 服务同时唤醒。跨零点第一次访问时 learning/statuses 会返回
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

// warmupWebPaths 是 web.shanbay.com 域(而非 apiv3)的页面 GET。与上面的 apiv3
// dashboard warmup 不同:学习数据 lazy init 的解冻触发点有一部分在 web 域服务端
// (为该 cookie 用户预热),apiv3 侧怎么戳都触达不到。
//
// 观测证据:每次用 AI agent 排查"卡在加载中"时,agent 会用浏览器/curl 带 cookie
// 访问这些 web 域页面(wordsweb SPA、登录后主页等),跑完之后 sb-cli 再跑 apiv3
// 就不再 412。而 sb-cli 原本只 warmup apiv3、从不碰 web 域,所以跨零点仍卡。
// 这里把这批 web 域 GET 补进 warmup,模仿 agent 排查时那一下"正确的初始化行为"。
//
// 只读、带 cookie+UA、错误全吞——价值在副作用不在返回;走独立 http.Client 不
// 经 apiv3 BaseURL,因为这是另一个域。
var warmupWebPaths = []string{
	"https://web.shanbay.com/wordsweb/",      // 学习 SPA 入口
	"https://web.shanbay.com/web/main/index", // 登录后主页
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
	c.warmupWeb()
}

// warmupWeb fires the web.shanbay.com page GETs concurrently (fire-and-forget,
// same 30s client timeout). These don't share apiv3's BaseURL — they hit the
// web domain directly with the cookie, to trigger the server-side warm-up
// that apiv3-only probes can't reach.
func (c *Client) warmupWeb() {
	cookie := c.creds.Cookie
	for _, u := range warmupWebPaths {
		go func(url string) {
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:152.0) Gecko/20100101 Firefox/152.0")
			req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
			req.Header.Set("Cookie", cookie)
			req.Header.Set("Referer", "https://web.shanbay.com/")
			_, _ = c.http.Do(req)
		}(u)
	}
}

// warmupOnce 让 retryNotReady 在初始 412 时再补一次 warmup,不重复。
type warmupOnce struct {
	once sync.Once
}

func (w *warmupOnce) fire(c *Client) {
	w.once.Do(c.Warmup)
}
