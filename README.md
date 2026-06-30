# shanbay-cli

终端背单词 CLI(扇贝单词)。逆向 `apiv3.shanbay.com` 私有接口,复用浏览器登录态。

> 完整调研/接口契约/解码算法见 `~/my-projects/plans/shanbay-cli/research.md`。

## 状态
- ✅ M0 响应解码移植(`internal/decode`),4 个真实加密向量 golden 测试通过。
- ✅ M1 认证 + API client(`internal/auth`、`internal/api`),`sb status` / `sb lookup` 实测可用。
- ✅ M2 提交契约(`PUT items/sync`,必须整轮完整集合 + 认识 schedule=3),真实联调通过。
- ✅ M3 学习引擎 `sb study`:拉队列→展示→评分→提交→进度递增,全链路实测。
- ✅ M5(主要)`sb goal` 每日目标、`--audio` 发音、多轮循环(背完自动下一组)。
- ✅ 登录:`sb login` 粘贴 curl 自动解析 cookie;运行时检测失效自动提示重登。
- ✅ M4 bubbletea TUI:`sb study` 默认进 TUI(单键 k/f、空格翻卡、p 发音、多轮自动续、带框卡片+完成统计);非 TTY / `--plain` / `--dry-run` 回退行模式。
- ⬜ 词书切换。

## 结构
```
cmd/sb/            CLI 入口(status / lookup)
internal/auth/     cookie 加载、CSRF 提取、JWT 过期检查
internal/api/      http client、端点、数据模型(集成解码)
internal/decode/   bayDecode 移植 + golden 测试(testdata/*.enc|.json)
```

## 使用
```bash
# 1. 登录:浏览器 F12 → 任意 apiv3.shanbay.com 请求 → 复制为 cURL,然后粘贴:
go run ./cmd/sb login   # 自动解析 cookie 存到 ~/.config/shanbay-cli/cookie(未登录/过期时其他命令也会自动提示)

# 2. 运行
go run ./cmd/sb status
go run ./cmd/sb lookup serendipity
go run ./cmd/sb goal                   # 看每日新词目标 + 可选值
go run ./cmd/sb goal 30                 # 设每日目标为 30
go run ./cmd/sb study                  # 背单词(终端里默认进 TUI:k/f 评分,空格翻卡)
go run ./cmd/sb study --review --audio # 同时复习 + 发音(需 mpg123/gst-play)
go run ./cmd/sb study --limit 10       # 本次最多背 10 个
go run ./cmd/sb study --plain          # 用简单行模式(或 --dry-run 只预览不写回)
# 临时指定 cookie 文件:
SHANBAY_COOKIE_FILE=/path/to/cookie go run ./cmd/sb status
```

## 测试
```bash
go test ./...            # 解码 golden + 拒绝明文
```

## 发布 (CI/CD)
- 每次 push / PR 到 `main`:`ci` 工作流跑 `go vet` / `go test` / `gofmt` 检查。
- 推送 `v*` tag:`release` 工作流交叉编译并发布到 GitHub Release:
  ```bash
  git tag v0.1.0 && git push origin v0.1.0
  ```
  产物:`sb-{linux,darwin}-{amd64,arm64}.tar.gz`(纯 Go,CGO 关闭)。
- 安装:从 Release 下载对应平台,解压后 `chmod +x sb-* && mv sb-* ~/go/bin/sb`(或任意 PATH 目录)。

## 注意
- cookie 含登录态,勿入 git;`auth_token`(JWT)约 3 个月过期,过期用 `sb login` 重新粘贴。
- 接口为私有逆向,扇贝改版可能失效;解码逻辑集中在 `internal/decode`,易替换。
- 仅个人账号自用。
