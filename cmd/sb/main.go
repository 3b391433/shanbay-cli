// Command sb is the shanbay vocabulary CLI. Bare `sb` runs `sb study`.
//
//	sb                        = sb study(默认背单词:每组 10、新词与复习混合)
//	sb login                  粘贴 curl,解析并保存登录态
//	sb status                 账号 / 当前词书 / 今日进度
//	sb lookup <word>          查词
//	sb goal [N]               查看/设置每日新词目标
//	sb study [flags]          背单词
//	    --new-only            只背新词(默认含复习)
//	    --order mixed|new-first  排列方式(默认 mixed 混合)
//	    --group N             每组单词数(默认 10,0=整队列)
//	    --limit N             本次最多 N 个(0=直到清空)
//	    --mute                关闭发音(默认开)
//	    --plain               简易行模式(非 TUI)
//	    --dry-run             只打印将提交的 body,不发送
//
// Login state is checked at startup: a missing or expired cookie triggers an
// interactive paste-a-curl login. A 401/403 mid-command also triggers re-login
// and a single retry.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/3b391433/shanbay-cli/internal/api"
	"github.com/3b391433/shanbay-cli/internal/audio"
	"github.com/3b391433/shanbay-cli/internal/auth"
	"github.com/3b391433/shanbay-cli/internal/keymap"
	"github.com/3b391433/shanbay-cli/internal/study"
	"github.com/3b391433/shanbay-cli/internal/tui"
)

func main() {
	// 裸 `sb` 或以 flag 开头(如 `sb --review`)默认走 study;否则首个参数是子命令。
	args := os.Args[1:]
	cmd, cmdArgs := "study", args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, cmdArgs = args[0], args[1:]
	}

	if cmd == "login" {
		doLogin()
		return
	}

	creds := ensureCreds()
	client := api.New(creds)

	run := func() error {
		switch cmd {
		case "status":
			return runStatus(client, creds)
		case "lookup":
			if len(cmdArgs) < 1 {
				return errors.New("usage: sb lookup <word>")
			}
			return runLookup(client, cmdArgs[0])
		case "goal":
			return runGoal(client, cmdArgs)
		case "study":
			return runStudy(client, cmdArgs)
		default:
			return fmt.Errorf("未知命令: %s(可用: study/status/lookup/goal/login)", cmd)
		}
	}

	err := run()
	if errors.Is(err, api.ErrUnauthorized) {
		fmt.Fprintln(os.Stderr, "\n检测到登录态失效,请重新登录。")
		creds = doLogin()
		client = api.New(creds)
		err = run()
	}
	if err != nil {
		fatal(err)
	}
}

// ensureCreds loads saved credentials; if absent or expired it runs login.
func ensureCreds() *auth.Credentials {
	creds, err := auth.Load("")
	if err == nil && !creds.Expired() {
		return creds
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "未检测到登录态(%v)。\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "登录态已过期(token 到 %s)。\n", creds.Expires.Format("2006-01-02"))
	}
	return doLogin()
}

// doLogin reads a pasted curl from stdin, parses + saves the cookie, validates
// it against the API, and returns the credentials.
func doLogin() *auth.Credentials {
	fmt.Println("请粘贴一条登录后的 curl(浏览器 F12 → 任意 apiv3.shanbay.com 请求 → 复制为 cURL):")
	fmt.Println("粘贴整段后,另起一空行回车结束 ↵")

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var b strings.Builder
	seen := false
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			if seen {
				break
			}
			continue
		}
		seen = true
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		fatal(err)
	}
	if !seen {
		fatal(errors.New("未读到任何输入"))
	}

	cookie, err := auth.ParseCookieFromCurl(b.String())
	if err != nil {
		fatal(fmt.Errorf("解析 curl 失败:%w", err))
	}
	path, err := auth.Save("", cookie)
	if err != nil {
		fatal(err)
	}
	creds, err := auth.Load(path)
	if err != nil {
		fatal(err)
	}

	// active check: hit an authenticated endpoint to confirm the cookie works
	if _, err := api.New(creds).CurrentBook(); err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			fatal(errors.New("cookie 无效或已过期 — 请用最新的登录后请求重试"))
		}
		fmt.Fprintf(os.Stderr, "⚠ 已保存,但验证请求出错:%v\n", err)
	}
	fmt.Printf("✓ 登录成功:%s(token 至 %s)→ 已保存到 %s\n",
		creds.Username, creds.Expires.Format("2006-01-02"), path)
	return creds
}

func runStatus(c *api.Client, creds *auth.Credentials) error {
	book, err := c.CurrentBook()
	if err != nil {
		return err
	}
	newGoal, err := c.LearningCount("NEW")
	if err != nil {
		return err
	}
	fmt.Printf("用户:     %s (id=%d)\n", creds.Username, creds.UserID)
	fmt.Printf("当前词书: %s [%s] — 共 %d 词\n", book.Materialbook.Name, book.MaterialbookID, book.Materialbook.TotalCount)
	fmt.Printf("今日:     新词 %d / 复习 %d / 已完成 %d\n", book.NewCount, book.ReviewCount, book.FinishedCount)
	fmt.Printf("每日目标: %d 个新词\n", newGoal)
	return nil
}

func runLookup(c *api.Client, word string) error {
	v, err := c.LookupVocab(word)
	if err != nil {
		return err
	}
	fmt.Printf("%s   US /%s/   UK /%s/\n", v.Word, v.Sound.IPAUS, v.Sound.IPAUK)
	if len(v.Senses) == 0 {
		fmt.Println("  (该接口未返回释义，释义走 vocab_senses)")
	}
	for _, s := range v.Senses {
		def := s.DefinitionCN
		if def == "" {
			def = s.DefinitionEN
		}
		fmt.Printf("  [%s] %s\n", s.POS, def)
	}
	return nil
}

func runGoal(c *api.Client, args []string) error {
	if len(args) == 0 {
		cur, err := c.LearningCount("NEW")
		if err != nil {
			return err
		}
		choices, err := c.CountChoices()
		if err != nil {
			return err
		}
		fmt.Printf("当前每日新词目标: %d\n可选值: %v\n用法: sb goal <N>\n", cur, choices)
		return nil
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return errors.New("用法: sb goal <数字>")
	}
	if err := c.SetDailyGoal(n); err != nil {
		return err
	}
	fmt.Printf("✓ 每日新词目标已设为 %d\n", n)
	return nil
}

func runStudy(c *api.Client, args []string) error {
	fs := flag.NewFlagSet("study", flag.ExitOnError)
	newOnly := fs.Bool("new-only", false, "只背新词(默认含复习)")
	order := fs.String("order", "mixed", "排列: mixed(混合) | new-first(先新后复)")
	group := fs.Int("group", 10, "每组单词数(0=整队列一次过)")
	limit := fs.Int("limit", 0, "本次最多背 N 个(0=直到清空)")
	dry := fs.Bool("dry-run", false, "只打印将提交的 body,不发送")
	mute := fs.Bool("mute", false, "关闭发音(默认开)")
	plain := fs.Bool("plain", false, "用简易行模式代替 TUI")
	_ = fs.Parse(args)

	review := !*newOnly
	mixed := *order != "new-first"
	useAudio := !*mute
	keys := keymap.Load()
	if useAudio && !audio.Available() {
		fmt.Fprintln(os.Stderr, "提示:未找到音频播放器,发音不可用(可 sudo apt install mpg123)。")
	}

	book, err := c.CurrentBook()
	if err != nil {
		return err
	}
	mbid := book.MaterialbookID

	// Default to the TUI in an interactive terminal; fall back to the line UI
	// for --plain, --dry-run, or when stdin is not a TTY (piped/CI).
	if !*plain && !*dry && isTTY() {
		prog := tea.NewProgram(tui.New(tui.Config{
			Client: c, MBID: mbid, BookName: book.Materialbook.Name,
			Review: review, Audio: useAudio, Limit: *limit,
			Group: *group, Mixed: mixed, Keys: keys,
		}))
		fm, err := prog.Run()
		if err != nil {
			return err
		}
		if mm, ok := fm.(tui.Model); ok {
			return mm.Err()
		}
		return nil
	}

	sc := bufio.NewScanner(os.Stdin)
	start := time.Now()
	totalGraded, totalKnown := 0, 0
	prevSig := ""

	for turn := 1; turn <= 100; turn++ {
		sess, err := study.Load(c, mbid, review)
		if err != nil {
			return err
		}
		cards := sess.Cards(review, mixed)
		if len(cards) == 0 {
			if turn == 1 {
				fmt.Println("队列为空 — 今天没有待学/待复习的词(或已全部完成)。")
			} else {
				fmt.Println("\n🎉 今日队列已清空。")
			}
			break
		}
		sig := sigOf(sess)
		if sig == prevSig {
			fmt.Println("\n队列未推进(上一组可能都标了不认识),停止。")
			break
		}

		prompt := cards
		if *group > 0 && *group < len(prompt) {
			prompt = prompt[:*group] // 每组只呈现 group 个
		}
		if *limit > 0 {
			rem := *limit - totalGraded
			if rem <= 0 {
				break
			}
			if rem < len(prompt) {
				prompt = prompt[:rem]
			}
		}

		if turn == 1 {
			fmt.Printf("词书《%s》。回车看释义,1=认识 2=不认识 3=太简单 q=结束并提交\n", book.Materialbook.Name)
		}
		fmt.Printf("\n—— 第 %d 组:%d 词(队列 新 %d / 复习 %d)——\n", turn, len(prompt), len(sess.AItems), len(sess.CItems))

		grades := map[string]study.Grade{}
		quit := false
		gradedThisTurn := 0
		for i, card := range prompt {
			fmt.Printf("[%d/%d] %s   /%s/\n", i+1, len(prompt), card.Word, card.IPAUS)
			if useAudio && card.AudioUS != "" {
				go audio.Play(card.AudioUS) //nolint:errcheck // best-effort
			}
			fmt.Print("       (回车看释义) ")
			if !sc.Scan() {
				quit = true
				break
			}
			for _, d := range card.Defs {
				fmt.Println("       " + d)
			}
			fmt.Print("       1=认识 2=不认识 3=太简单 q=退出: ")
			if !sc.Scan() {
				quit = true
				break
			}
			ans := strings.ToLower(strings.TrimSpace(sc.Text()))
			switch {
			case keymap.Has(keys.Quit, ans):
				quit = true
			case keymap.Has(keys.Known, ans):
				grades[card.ItemID] = study.Known
				gradedThisTurn++
			case keymap.Has(keys.TooEasy, ans):
				grades[card.ItemID] = study.TooEasy
				gradedThisTurn++
			default: // keys.Unknown 或其它输入 = 不认识
				gradedThisTurn++
			}
			if quit {
				break
			}
			fmt.Println()
		}
		if err := sc.Err(); err != nil {
			return err
		}
		if gradedThisTurn == 0 {
			if turn == 1 {
				fmt.Println("未评分,退出(不提交)。")
			}
			break
		}

		body := sess.BuildSubmit(grades, sess.LearningTime+int(time.Since(start).Seconds()))
		if *dry {
			pretty, _ := json.MarshalIndent(body, "", "  ")
			fmt.Println("\n--- DRY RUN: 第 1 组将提交的 body(未发送) ---")
			fmt.Println(string(pretty))
			return nil
		}
		if err := c.SubmitItems(mbid, body); err != nil {
			return err
		}
		nk := len(body.AItemsKnown) + len(body.CItemsKnown)
		totalGraded += gradedThisTurn
		totalKnown += nk
		fmt.Printf("  ✓ 第 %d 组已提交(认识/掌握 %d)\n", turn, nk)

		if quit {
			break
		}
		if *limit > 0 && totalGraded >= *limit {
			break
		}
		prevSig = sig
	}

	if totalGraded > 0 {
		fmt.Printf("\n本次共评分 %d 词(认识 %d)。\n", totalGraded, totalKnown)
		if st, err := c.BookStatus(mbid); err == nil {
			fmt.Printf("当前进度:新词 %d/%d,复习 %d/%d,剩余 %d\n",
				st.AFinishedCount, st.ACount, st.CFinishedCount, st.CCount, st.RemainingCount)
		}
	}
	return nil
}

// sigOf is a stable signature of the current not-finished set, used to detect
// when a turn made no progress (to stop the loop).
func sigOf(s *study.Session) string {
	ids := make([]string, 0, len(s.AItems)+len(s.CItems))
	for _, it := range s.AItems {
		ids = append(ids, "a:"+it.ItemID)
	}
	for _, it := range s.CItems {
		ids = append(ids, "c:"+it.ItemID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
