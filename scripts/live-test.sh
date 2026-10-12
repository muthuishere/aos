#!/usr/bin/env bash
# live-test.sh — focus-independent feature pass for aos on macOS.
#
# Runs while a person keeps working on their other monitors: every assertion is
# on machine-checkable state (JSON results, text read back through
# accessibility, the window list, the recording's events.json), never on what
# is frontmost or on screenshots. Everything happens on ONE monitor (default 1,
# the laptop screen); nothing is opened, drawn or recorded anywhere else.
#
#   scripts/live-test.sh [path/to/aos]        # default: aos on PATH
#   AOS_TEST_MONITOR=1 AOS_TEST_KEEP=1 scripts/live-test.sh ./aos
#
# Prints PASS/FAIL per step and exits non-zero if any step failed.
set -u

AOS_BIN=${1:-aos}
MON=${AOS_TEST_MONITOR:-1}
export AOS_NO_SKILL_AUTOINSTALL=1
WORK=$(mktemp -d /tmp/aos-live.XXXXXX)
DOC=$WORK/aos-live-test.txt
OUT=$WORK/live.mp4
FAILS=0
STEP=0

aos() { "$AOS_BIN" "$@"; }

pass() { STEP=$((STEP + 1)); printf 'PASS  %2d  %s\n' "$STEP" "$1"; }
fail() { STEP=$((STEP + 1)); FAILS=$((FAILS + 1)); printf 'FAIL  %2d  %s\n' "$STEP" "$1"; [ -n "${2:-}" ] && printf '          %s\n' "$2"; }

# jcheck <name> <json> <python expression over j (the parsed json) and M (monitor rect)>
jcheck() {
	local name=$1 json=$2 expr=$3 out
	if out=$(printf '%s' "$json" | python3 -c '
import json, sys
M = dict(zip("xywh", map(int, sys.argv[2].split(","))))
def inside(r, m=M):
    return r["X"] >= m["x"] and r["Y"] >= m["y"] and r["X"] + r["Width"] <= m["x"] + m["w"] and r["Y"] + r["Height"] <= m["y"] + m["h"]
raw = sys.stdin.read().strip()
try:
    j = json.loads(raw)
except ValueError:
    try:
        j = json.loads(raw.splitlines()[-1])  # a log line before the JSON
    except Exception:
        print(raw[:300]); sys.exit(1)
try:
    ok = eval(sys.argv[1])
except Exception as e:
    ok = False; raw = f"{type(e).__name__}: {e}: {raw}"
if not ok:
    print(raw[:300]); sys.exit(1)
' "$expr" "$MRECT" 2>&1); then pass "$name"; else fail "$name" "$out"; fi
}

focused_id() { aos window list --json | python3 -c 'import json,sys; print(next((w["ID"] for w in json.load(sys.stdin) if w.get("Focused")), ""))'; }
win_json() { aos window list --json | python3 -c 'import json,sys; t=sys.argv[1]; print(json.dumps(next((w for w in json.load(sys.stdin) if t in w["Title"] or w["App"]==t), {})))' "$1"; }
running() { pgrep -xq "$1"; }

# ---- monitor ---------------------------------------------------------------
MRECT=$(aos display list | python3 -c '
import re, sys
mon = sys.argv[1]
for line in sys.stdin:
    m = re.match(r"\s*(\d+)\s+(\d+)x(\d+)([+-]\d+)([+-]\d+)", line)
    if m and m.group(1) == mon:
        print(f"{int(m.group(4))},{int(m.group(5))},{m.group(2)},{m.group(3)}")
' "$MON")
if [ -z "$MRECT" ]; then echo "monitor $MON not found"; exit 2; fi
IFS=, read -r MX MY MW MH <<<"$MRECT"
echo "aos live test on monitor $MON ($MRECT), work dir $WORK"

TEXTEDIT_WAS_RUNNING=0; running TextEdit && TEXTEDIT_WAS_RUNNING=1
CALC_WAS_RUNNING=0; running Calculator && CALC_WAS_RUNNING=1
CHROME_RUNNING=0; running "Google Chrome" && CHROME_RUNNING=1

cleanup() {
	aos capture video status 2>/dev/null | grep -q recording && aos capture video stop --raw >/dev/null 2>&1
	aos draw stop >/dev/null 2>&1
	aos subtitle stop >/dev/null 2>&1
	osascript -e "tell application \"TextEdit\" to close (every document whose name is \"aos-live-test.txt\") saving no" >/dev/null 2>&1
	if [ $TEXTEDIT_WAS_RUNNING = 0 ]; then osascript -e 'tell application "TextEdit" to quit saving no' >/dev/null 2>&1; fi
	if [ $CALC_WAS_RUNNING = 0 ]; then osascript -e 'tell application "Calculator" to quit' >/dev/null 2>&1; fi
	if [ $CHROME_RUNNING = 1 ]; then osascript -e 'tell application "Google Chrome" to close (every window whose title contains "AOSCLOSETEST")' >/dev/null 2>&1; fi
	[ -z "${AOS_TEST_KEEP:-}" ] && rm -rf "$WORK"
}
trap cleanup EXIT

# ---- open the windows this test needs, on the test monitor ------------------
: >"$DOC"
open -g -a TextEdit "$DOC"   # -g: in the background, nobody's focus is taken
open -g -a Calculator
aos window wait --title=aos-live-test.txt --timeout=10000 >/dev/null 2>&1
aos window wait Calculator --timeout=10000 >/dev/null 2>&1
aos window move --title=aos-live-test.txt --monitor="$MON" --at=40,80,700,480 >/dev/null
aos window move Calculator --monitor="$MON" --at=800,80,230,400 >/dev/null
sleep 0.5
jcheck "TextEdit test document is on monitor $MON" "$(win_json aos-live-test.txt)" "j.get('Monitor') == $MON and inside(j['Bounds'])"
jcheck "Calculator is on monitor $MON" "$(win_json Calculator)" "j.get('Monitor') == $MON and inside(j['Bounds'])"
DOC_ID=$(win_json aos-live-test.txt | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ID",""))')

# ---- recording ---------------------------------------------------------------
REC=$(aos capture video start smart --monitor="$MON" --out="$OUT" --max=3m --json 2>&1)
jcheck "smart recording started on monitor $MON" "$REC" "j['Mode'] == 'smart' and j['Capture'] == {'X': $MX, 'Y': $MY, 'Width': $MW, 'Height': $MH}"
aos capture video status | grep -q "$OUT" && pass "recording is live" || fail "recording is live" "$(aos capture video status 2>&1)"

# ---- 1. focus-free typing, verified by state ---------------------------------
BEFORE=$(focused_id)
R=$(aos key type "aos live test" --app=TextEdit --verify --json 2>&1)
jcheck "key type --verify into TextEdit (posted, text read back)" "$R" "j['Route'] == 'post' and j['Verified'] and 'aos live test' in j['Value'].lower() and j['Window']['ID'] == '$DOC_ID'"
R=$(aos key press enter --app=TextEdit --json 2>&1)
jcheck "key press enter into TextEdit (posted)" "$R" "j['Route'] == 'post'"
R=$(aos key type "second line" --app=TextEdit --verify --json 2>&1)
jcheck "second line verified after enter" "$R" "j['Verified'] and j['Value'].lower().replace(chr(13), chr(10)).endswith('aos live test' + chr(10) + 'second line')"
AFTER=$(focused_id)
if [ "$AFTER" != "$DOC_ID" ] && { [ "$BEFORE" = "$AFTER" ] || [ "$BEFORE" != "$DOC_ID" ]; }; then
	pass "TextEdit never took OS focus (focused: $AFTER)"
else
	fail "TextEdit never took OS focus" "before=$BEFORE after=$AFTER doc=$DOC_ID"
fi
R=$(aos key type "x" --app=NoSuchAppAOS --verify 2>&1); RC=$?
[ $RC = 3 ] && pass "unknown target exits 3, types nothing" || fail "unknown target exits 3" "rc=$RC $R"

# ---- 2. Calculator: posted keys + accessibility press ----------------------
aos key press esc --app=Calculator >/dev/null
aos key press esc --app=Calculator >/dev/null
R=$(aos key type "12+30=" --app=Calculator --json 2>&1)
jcheck "key type 12+30= into Calculator (posted)" "$R" "j['Route'] == 'post'"
sleep 0.4
R=$(aos find --app=Calculator --text=42 --source=accessibility --exact --no-raise --json 2>&1)
jcheck "Calculator shows 42 (accessibility tree)" "$R" "j['Text'] == '42' or j['Label'].strip(chr(0x200e)) == '42'"
R=$(aos mouse click --app=Calculator --find=7 --role=button --json 2>&1)
jcheck "mouse click --find=7 used AXPress, not the pointer" "$R" "j['Route'] == 'press' and inside(j['Found']['Bounds'])"
sleep 0.4
R=$(aos find --app=Calculator --text=7 --role=text --source=accessibility --exact --no-raise --json 2>&1)
jcheck "Calculator display now reads 7" "$R" "j['Role'] == 'text'"

# ---- 3. overlays land on the recording's monitor ------------------------------
R=$(aos subtitle show "aos live test: subtitle" --seconds=2 --json 2>&1)
jcheck "subtitle (no --monitor) on the recording's monitor" "$R" "j['Monitor'] == $MON and inside(j['Sign'])"
R=$(aos draw text "aos live test: label" --duration=1 --json 2>&1)
jcheck "draw text (no --monitor) on the recording's monitor" "$R" "j['Monitor'] == $MON and inside(j['Sign'])"
R=$(aos draw --at=300,300 --text="relative point" --duration=1 --json 2>&1)
jcheck "draw --at is relative to the recording's monitor" "$R" "j['Monitor'] == $MON and j['Tip']['X'] == $MX + 300 and j['Tip']['Y'] == $MY + 300"
R=$(aos draw --app=Calculator --find=7 --role=button --style=ring --text="press 7" --duration=1 --json 2>&1)
jcheck "draw --find on Calculator stays on monitor $MON" "$R" "j['Monitor'] == $MON and inside(j['Sign']) and inside(j['Found']['Bounds'])"

# ---- 4. window list IDs confirm a close -------------------------------------
if [ $CHROME_RUNNING = 1 ]; then
	osascript -e 'tell application "Google Chrome"
		set w to make new window
		set URL of active tab of w to "data:text/html,<title>AOSCLOSETEST</title><h1>aos close test</h1>"
	end tell' >/dev/null 2>&1
	aos window wait --title=AOSCLOSETEST --timeout=10000 >/dev/null 2>&1
	aos window move --title=AOSCLOSETEST --monitor="$MON" --at=60,560,600,400 >/dev/null 2>&1
	CID=$(win_json AOSCLOSETEST | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ID",""))')
	[ -n "$CID" ] && pass "throwaway Chrome window AOSCLOSETEST listed (id $CID)" || fail "throwaway Chrome window listed"
	osascript -e 'tell application "Google Chrome" to close (every window whose title contains "AOSCLOSETEST")' >/dev/null 2>&1
	sleep 1
	if [ -n "$CID" ] && ! aos window list --json | python3 -c 'import json,sys; sys.exit(0 if any(w["ID"]==sys.argv[1] for w in json.load(sys.stdin)) else 1)' "$CID"; then
		pass "closed window's ID is gone from window list --json"
	else
		fail "closed window's ID is gone from window list --json"
	fi
else
	pass "Chrome not running: close test skipped (never launched for a test)"
fi

# ---- 5. the recording's events ---------------------------------------------
aos window move --title=aos-live-test.txt --monitor="$MON" --at=60,100,680,460 >/dev/null  # a move of the target
sleep 1.2
STOP=$(aos capture video stop --json 2>&1)
jcheck "recording stopped and final cut rendered" "$STOP" "'Final' in j and j['Duration'] > 2"
EVENTS=$(printf '%s' "$STOP" | python3 -c 'import json,sys; print(json.loads(sys.stdin.read().strip().splitlines()[-1])["Events"])' 2>/dev/null)
if [ -f "$EVENTS" ]; then
	jcheck "events: capture area is monitor $MON" "$(cat "$EVENTS")" "j['capture'] == {'X': $MX, 'Y': $MY, 'Width': $MW, 'Height': $MH}"
	jcheck "events: every rect is inside monitor $MON" "$(cat "$EVENTS")" "all(inside(e['rect']) for e in j['events'] if e.get('rect') and e['kind'] != 'cursor')"
	jcheck "events: camera windows are only aos targets" "$(cat "$EVENTS")" "{e.get('label') for e in j['events'] if e['kind'] == 'window'} <= {'TextEdit', 'Calculator'} and any(e['kind'] == 'window' and e.get('label') == 'Calculator' for e in j['events'])"
	jcheck "events: key/click/draw actions were logged" "$(cat "$EVENTS")" "{'key type', 'mouse click', 'draw'} <= {e.get('cmd') for e in j['events']}"
	jcheck "events: the target's move was logged" "$(cat "$EVENTS")" "any(e['kind'] == 'window' and e.get('label') == 'TextEdit' and e['rect']['X'] == $MX + 60 for e in j['events'])"
else
	fail "events.json exists" "$STOP"
fi
FINAL=$(printf '%s' "$STOP" | python3 -c 'import json,sys; print(json.loads(sys.stdin.read().strip().splitlines()[-1]).get("Final",""))' 2>/dev/null)
[ -n "$FINAL" ] && { [ -n "${AOS_TEST_KEEP:-}" ] && echo "final cut: $FINAL" || echo "final cut rendered (set AOS_TEST_KEEP=1 to keep it)"; }

echo
if [ $FAILS = 0 ]; then echo "ALL $STEP STEPS PASSED"; else echo "$FAILS OF $STEP STEPS FAILED"; fi
[ $FAILS = 0 ]
