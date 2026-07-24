#!/usr/bin/env bash
# 跨零点"卡在加载中"诊断:逐个访问 web.shanbay.com 域页面,每次后探测 apiv3
# learning/statuses 是否仍 412,定位真正能让后端解冻的 warmup 路径。
#
# 用法: sb 卡在加载中时,另开终端跑
#   bash scripts/diag-412.sh
# 或指定 mbid:
#   bash scripts/diag-412.sh <material_book_id>
#
# 输出每个 web 路径 GET 前后 apiv3 statuses 的 HTTP code:
#   412 = 后端还没准备好(没解冻)
#   200 = 已解冻(上一个 web GET 就是起作用的那个)
set -u

COOKIE_FILE="${SHANBAY_COOKIE:-$HOME/.config/shanbay-cli/cookie}"
if [[ ! -f "$COOKIE_FILE" ]]; then
	echo "cookie 不存在: $COOKIE_FILE"; exit 1
fi
COOKIE=$(cat "$COOKIE_FILE")
UA="Mozilla/5.0 (X11; Linux x86_64; rv:152.0) Gecko/20100101 Firefox/152.0"
CSRF=$(printf '%s' "$COOKIE" | grep -oP 'csrftoken=\K[^;]+' || true)

API="https://apiv3.shanbay.com"
curl_api() { # $1 = path
	curl -sS -o /dev/null -w "%{http_code}" --connect-timeout 5 -m 10 \
		"$API$1" -H "User-Agent: $UA" -H "X-CSRFToken: $CSRF" \
		-H "Origin: https://web.shanbay.com" -H "Referer: https://web.shanbay.com/" \
		-H "Cookie: $COOKIE" 2>/dev/null
}
curl_web() { # $1 = full url
	curl -sS -o /dev/null -w "%{http_code}" --connect-timeout 5 -m 10 \
		"$1" -H "User-Agent: $UA" -H "Referer: https://web.shanbay.com/" \
		-H "Cookie: $COOKIE" 2>/dev/null
}

# 取 mbid
MBID="${1:-}"
if [[ -z "$MBID" ]]; then
	echo "取 current book…"
	code=$(curl_api "/wordsapp/user_material_books/current")
	echo "  current -> $code"
	if [[ "$code" == "200" ]]; then
		body=$(curl -sS --connect-timeout 5 -m 10 "$API/wordsapp/user_material_books/current" \
			-H "User-Agent: $UA" -H "X-CSRFToken: $CSRF" -H "Cookie: $COOKIE" 2>/dev/null)
		MBID=$(printf '%s' "$body" | grep -oP '"id"\s*:\s*\K[0-9]+' | head -1)
	fi
fi
if [[ -z "$MBID" ]]; then
	echo "拿不到 mbid(apiv3 可能也 412/401)。请手动传: bash $0 <mbid>"; exit 1
fi
echo "mbid = $MBID"

probe() { # 探测 learning/statuses
	curl_api "/wordsapp/user_material_books/$MBID/learning/statuses"
}

echo
echo "==== 初始 apiv3 statuses ===="
echo "  statuses -> $(probe)"
echo

WEB_PATHS=(
	"https://web.shanbay.com/wordsweb/"
	"https://web.shanbay.com/wordsweb/#/study/entry"
	"https://web.shanbay.com/web/main/index"
	"https://web.shanbay.com/"
	"https://web.shanbay.com/bdc/learning"
	"https://web.shanbay.com/learning-space/home"
	"https://www.shanbay.com/"
)

for path in "${WEB_PATHS[@]}"; do
	echo "==== GET $path ===="
	echo "  web -> $(curl_web "$path")"
	sleep 3
	echo "  apiv3 statuses now -> $(probe)"
	echo
done

echo "==== done。第一个让 statuses 从 412→200 的 web 路径就是该补进 warmupWebPaths 的 ===="
