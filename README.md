# shanbay-cli (`sb`)

> 在终端里背扇贝单词。

`sb` 是一个命令行背单词工具:看词 → 判断「认识 / 不认识 / 太简单」→ 自动揭晓中文释义和例句 → 进度实时同步回你的[扇贝单词](https://web.shanbay.com/wordsweb/)账号。带发音、复习、每日目标,全程键盘操作。

> ⚠️ 非官方工具,基于扇贝 web 端私有接口实现,仅供个人账号自用学习。接口可能随扇贝改版失效。

## 功能

- 🎴 **卡片式 TUI**:单键评分(`k` 认识 / `f` 不认识 / `e` 太简单),自动翻到下一词
- 🔊 **自动发音**:出词读单词美音,揭晓后读例句
- 📖 **释义 + 例句**:评分后展示中文释义和英/中例句
- 🔁 **新词 + 复习**:一组背完自动续下一组,直到当天清空
- ➕ **再来一组**:当日计划背完后可继续加量学新词(每日最多 3 组)
- 🎯 **每日目标**可调;「太简单」标记已掌握、不再复习
- ☁️ 进度实时写回扇贝账号
- ✅ **自动打卡**:背完当日任务自动打卡(等同网页「去打卡」),保持连续天数;也可 `sb checkin` 手动打卡

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

> 发音可选,依赖系统播放器(**推荐 `ffplay` 或 `mpv`**;详见 [发音与音频播放器](#发音与音频播放器))。没有则静默,不影响背单词。

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
sb --no-checkin          # 背完后不自动打卡(默认完成即打卡)

sb goal 30               # 每日新词目标设为 30(sb goal 查看)
sb lookup serendipity    # 查词
sb status                # 账号 / 当前词书 / 今日进度
sb checkin               # 手动打卡(完成今日任务后;显示累计天数)
```

> **自动打卡**:背完当日任务后 `sb` 会自动打卡(等同网页 [study/entry](https://web.shanbay.com/wordsweb/#/study/entry) 的「去打卡」)。是否可打卡由扇贝服务端按当日完成情况判定——任务没做完或当天已打过卡都会静默跳过,不会重复打卡。`--no-checkin` 关闭自动打卡,`--dry-run` 也不打卡。

TUI 里的按键:

| 键 | 作用 |
|---|---|
| `1` / `k` | 认识 |
| `2` / `f` | 不认识 |
| `3` / `e` | 太简单(标记已掌握,不再复习) |
| `空格` / `↵` | 看完释义例句后,进入下一词 |
| `1` `2` `3` | (揭晓后)对当前词改判 |
| `0` / `p` | 重新发音 |
| `esc` / `q` | 结束并提交进度 |

> **中文输入法用户**:字母键(k/f/e/p/q)会被拼音组字拦截(变成带下划线的预选)。请用**数字键 `1`/`2`/`3` 评分、`空格`下一词、`0`发音、`esc`退出**——这些键不触发输入法,无需切到英文。

### 自定义按键

按键绑定存于 `~/.config/shanbay-cli/keys.json`(首次运行 `sb study` 自动生成默认),每个动作可绑定多个键,改完下次运行 `sb` 即生效。动作名与默认键:

| JSON 动作 | 默认键 | 作用 |
|---|---|---|
| `known` | `1` `k` | 认识 |
| `unknown` | `2` `f` | 不认识 |
| `too_easy` | `3` `e` | 太简单(标记已掌握) |
| `next` | `enter` `空格` | 揭晓后进入下一词 |
| `audio` | `0` `p` | 重新发音 |
| `quit` | `esc` `q` | 结束并提交 |

**列出某个动作会整个替换它的默认键**,所以想「加一个键」时要把默认键一起写上。例如让**空格 / 回车也能判「认识」**(保留 `1`/`k`),同时空格 / 回车在揭晓后仍是「下一词」:

```json
{
  "known": ["1", "k", "enter", " "],
  "next":  ["enter", " "]
}
```

(未写出的动作沿用默认。)键名:字母 / 数字原样;特殊键用 `enter` / `esc` / `up` / `down` / `left` / `right` / `tab`;空格写成 `" "` 或 `"space"`。同一个键可在不同状态复用——上例里 `enter` / 空格在提问时是「认识」、揭晓后是「下一词」(揭晓后 `next` 优先于改判)。

切换词书暂未支持,请在扇贝 App / 网页上切换;`sb` 跟随当前词书。

## 发音与音频播放器

发音是可选功能:`sb` 调用系统里**第一个可用**的命令行播放器放音频(都没有则静默,不影响背单词)。按下列优先级探测:

| 播放器 | 格式 | 说明 |
|---|---|---|
| `ffplay`(来自 ffmpeg) | MP3 + AAC | 推荐 |
| `mpv` | MP3 + AAC | 推荐 |
| `gst-play-1.0`(GStreamer) | MP3 + AAC | 可用 |
| `mpg123` | **仅 MP3** | 兜底;放不了 AAC |

> ⚠️ 音频以 **MP3 为主,少数片段是 AAC**(实测部分例句)。`mpg123` 只能解 MP3,遇到 AAC 片段会解成噪音;只装它时绝大多数发音正常、偶发 AAC 片段失真——想全部正常请装 `ffplay` 或 `mpv`。

安装(任选其一):

```bash
sudo apt install ffmpeg      # Debian/Ubuntu(提供 ffplay);或 sudo apt install mpv
sudo dnf install ffmpeg      # Fedora
sudo pacman -S ffmpeg        # Arch
brew install ffmpeg          # macOS
```

### WSL 下音频卡顿 / 电音?

WSL2(WSLg)把声音经 RDP 转发给 Windows,缓冲欠载会造成卡顿、爆音、金属电音——这是 [WSLg 的已知问题](https://github.com/microsoft/wslg/issues/908)(原生 Linux 无此问题,与本工具无关)。缓解:

```bash
# 1) 关掉 systemd-timesyncd:其时钟校正会让 PulseAudio 欠载
#    (WSL 仍从 Windows 宿主取时间,系统时钟不受影响)
sudo systemctl disable --now systemd-timesyncd

# 2) 加大 PulseAudio 客户端缓冲(写进 ~/.bashrc,重开终端生效)
echo 'export PULSE_LATENCY_MSEC=300' >> ~/.bashrc
```

仍不理想可把 `PULSE_LATENCY_MSEC` 调更大(如 `500`),或改用 `mpv`(其 `--audio-buffer` 缓冲更稳)。要彻底绕开 RDP,可在 Windows 侧跑 [pulseaudio-win32](https://github.com/pgaskin/pulseaudio-win32) 并让 WSL 用 TCP 直连(代价是延迟略高)。

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
