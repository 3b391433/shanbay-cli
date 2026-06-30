// Command sb is the shanbay vocabulary CLI.
//
//	sb login                  paste a curl; parse + save the login cookie
//	sb status                 account + current book + today's counts
//	sb lookup <word>          dictionary lookup (decoded)
//	sb goal [N]               show daily new-word goal, or set it
//	sb study [flags]          interactive study loop (loops through turns)
//	    --review              also present review words for grading
//	    --limit N             grade at most N words this run (0 = until done)
//	    --mute                disable pronunciation (auto-plays by default)
//	    --dry-run             print the first turn's submit body, do NOT send
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

	"shanbay-cli/internal/api"
	"shanbay-cli/internal/audio"
	"shanbay-cli/internal/auth"
	"shanbay-cli/internal/study"
	"shanbay-cli/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sb <login|status|lookup|goal|study> ...")
		os.Exit(2)
	}
	cmd := os.Args[1]

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
			if len(os.Args) < 3 {
				return errors.New("usage: sb lookup <word>")
			}
			return runLookup(client, os.Args[2])
		case "goal":
			return runGoal(client, os.Args[2:])
		case "study":
			return runStudy(client, os.Args[2:])
		default:
			return fmt.Errorf("unknown command: %s", cmd)
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
	review := fs.Bool("review", false, "also present review words for grading")
	limit := fs.Int("limit", 0, "grade at most N words this run (0 = until done)")
	dry := fs.Bool("dry-run", false, "print the first turn's submit body, do not send")
	mute := fs.Bool("mute", false, "disable pronunciation (on by default)")
	plain := fs.Bool("plain", false, "use the simple line UI instead of the TUI")
	_ = fs.Parse(args)

	useAudio := !*mute
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
			Review: *review, Audio: useAudio, Limit: *limit,
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
		sess, err := study.Load(c, mbid, *review)
		if err != nil {
			return err
		}
		cards := sess.Cards(*review)
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
			fmt.Printf("词书《%s》。回车看释义,k=认识  f=不认识  e=太简单  q=结束并提交本组\n", book.Materialbook.Name)
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
			fmt.Print("       k=认识  f=不认识  e=太简单  q=退出: ")
			if !sc.Scan() {
				quit = true
				break
			}
			switch strings.ToLower(strings.TrimSpace(sc.Text())) {
			case "q":
				quit = true
			case "k":
				grades[card.ItemID] = study.Known
				gradedThisTurn++
			case "e":
				grades[card.ItemID] = study.TooEasy
				gradedThisTurn++
			default:
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
