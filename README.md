# shanbay-cli (`sb`)

> 在终端里背扇贝单词。

`sb` 是一个命令行背单词工具:看词 → 判断「认识 / 不认识 / 太简单」→ 自动揭晓中文释义和例句 → 进度实时同步回你的[扇贝单词](https://web.shanbay.com/wordsweb/)账号。带发音、复习、每日目标,全程键盘操作。

> ⚠️ 非官方工具,基于扇贝 web 端私有接口实现,仅供个人账号自用学习。接口可能随扇贝改版失效。

## 功能

- 🎴 **卡片式 TUI**:单键评分(`k` 认识 / `f` 不认识 / `e` 太简单),自动翻到下一词
- 🔊 **自动发音**:出词读单词美音,揭晓后读例句
- 📖 **释义 + 例句**:评分后展示中文释义和英/中例句
- 🔁 **新词 + 复习**:一组背完自动续下一组,直到当天清空
- 🎯 **每日目标**可调;「太简单」标记已掌握、不再复习
- ☁️ 进度实时写回扇贝账号

## 安装

### 下载预编译二进制(推荐)

从 [Releases](https://github.com/3b391433/shanbay-cli/releases/latest) 下载对应平台并放进 `PATH`:

```bash
# Linux x86_64 示例(其它平台把文件名换成对应的)
curl -fL -o sb.tar.gz https://github.com/3b391433/shanbay-cli/releases/latest/download/sb-linux-amd64.tar.gz
tar -xzf sb.tar.gz
install -m 0755 sb-linux-amd64 ~/.local/bin/sb     # 确保 ~/.local/bin 在 PATH
```

平台文件名:`sb-linux-amd64` · `sb-linux-arm64` · `sb-darwin-amd64`(Intel Mac)· `sb-darwin-arm64`(Apple 芯片)。

### 用 Go 安装

```bash
go install github.com/3b391433/shanbay-cli/cmd/sb@latest
```

### 从源码构建

```bash
git clone https://github.com/3b391433/shanbay-cli && cd shanbay-cli
go build -o sb ./cmd/sb
```

> 发音可选,依赖系统播放器之一:`mpg123` / `ffplay` / `mpv` / `gst-play-1.0`(没有则静默,不影响背单词)。

## 快速开始

首次使用先登录(复用你浏览器的扇贝登录态):

```bash
sb login
```

按提示操作:浏览器打开并登录 [web.shanbay.com](https://web.shanbay.com/wordsweb/) → 按 `F12` 打开开发者工具 → 「网络」面板里随便找一条 `apiv3.shanbay.com` 的请求 → 右键「复制为 cURL」→ 粘贴回终端,空行回车结束。

`sb` 会自动解析其中的 cookie 并保存到 `~/.config/shanbay-cli/cookie`。登录态失效时任意命令也会自动提示重新粘贴。

## 用法

```bash
sb                       # = sb study:背单词。默认每组 10 个、新词与复习混合穿插
sb --new-only            # 只背新词(默认含复习)
sb --group 15            # 每组 15 个(默认 10;0 = 整队列一次过)
sb --order new-first     # 先背新词再复习(默认 mixed 混合)
sb --limit 20            # 本次最多背 20 个
sb --mute                # 关闭发音(默认开)
sb --plain               # 简易行模式(非 TUI)

sb goal 30               # 每日新词目标设为 30(sb goal 查看)
sb lookup serendipity    # 查词
sb status                # 账号 / 当前词书 / 今日进度
```

TUI 里的按键:

| 键 | 作用 |
|---|---|
| `k` | 认识 |
| `f` | 不认识 |
| `e` | 太简单(标记已掌握,不再复习) |
| `空格` / `↵` | 看完释义例句后,进入下一词 |
| `k` `f` `e` | (揭晓后)对当前词改判 |
| `p` | 重新发音 |
| `q` | 结束并提交进度 |

切换词书暂未支持,请在扇贝 App / 网页上切换;`sb` 跟随当前词书。

## 注意

- cookie 是你的登录凭据,**不要分享或提交到 git**。`auth_token` 约 3 个月过期,过期后 `sb login` 重新粘贴即可。
- 仅供个人账号自用;请勿用于批量请求或绕过扇贝的限制。

## 开发

```bash
go test ./...     # 单元测试(解码 golden、提交语义、TUI 逻辑,均离线)
go vet ./...
```

CI/CD(GitHub Actions):push / PR 跑 `ci`(vet + test + gofmt);推 `v*` tag 触发 `release`,自动交叉编译 4 个平台并发布到 Releases。

解码算法参考自 [honwhy/shanbay-ext](https://github.com/honwhy/shanbay-ext)。
