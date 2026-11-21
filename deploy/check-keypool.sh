#!/usr/bin/env bash
# =============================================================================
# 号池渠道体检脚本
#
# 用法（在服务器上执行）：
#   bash check-keypool.sh                 # 自动定位数据库
#   bash check-keypool.sh /path/new-api.db
#
# 输出每个号池渠道的关键体检项，并明确指出哪一项不对。
# 只读，不修改任何数据。
# =============================================================================
set -u

DB="${1:-}"
if [ -z "$DB" ]; then
  for cand in /data/new-api.db ./data/new-api.db ./new-api.db ../new-api.db; do
    if [ -f "$cand" ]; then DB="$cand"; break; fi
  done
fi
if [ -z "$DB" ] || [ ! -f "$DB" ]; then
  echo "❌ 找不到数据库文件。请手动指定路径：bash check-keypool.sh /path/new-api.db"
  exit 1
fi

echo "数据库：$DB"
echo

# 用 sqlite3 查；没有 sqlite3 就退回 python3。
if command -v sqlite3 >/dev/null 2>&1; then
  Q() { sqlite3 "$DB" "$1"; }
elif command -v python3 >/dev/null 2>&1; then
  Q() { python3 - "$DB" "$1" <<'PY'
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
for row in conn.execute(sys.argv[2]):
    print("|".join("" if v is None else str(v) for v in row))
PY
  }
else
  echo "❌ 需要 sqlite3 或 python3 才能执行体检。"
  exit 1
fi

echo "===================== 号池渠道体检 ====================="
# key 字段本身含换行，不能直接 SELECT 出来（会把输出行拆散、导致字段错位），
# 改成在 SQL 里把换行数换算成 key 条数。
ROWS="$(Q "SELECT id, name, IFNULL(base_url,'(未设置)'), IFNULL(channel_info,'{}'), IFNULL(\"group\",''), IFNULL(models,''), CASE WHEN IFNULL(key,'')='' THEN 0 ELSE LENGTH(key)-LENGTH(REPLACE(key, CHAR(10),''))+1 END FROM channels WHERE tag LIKE 'keypool%' ORDER BY id;")"

if [ -z "$ROWS" ]; then
  echo "⚠️  库里没有任何 tag 以 keypool% 开头的渠道。"
  echo "   说明号池渠道根本没建出来（可能被手工改过 tag，或建渠道时接口报错）。"
  exit 0
fi

echo "$ROWS" | while IFS='|' read -r id name base info grp models keycnt; do
  [ -z "$id" ] && continue
  echo "──────────────────────────────────────────"
  echo "渠道 #${id}  ${name}"
  echo "  分组(group)   : ${grp}"
  echo "  BaseURL       : ${base}"
  echo "  models        : ${models:0:120}$( [ ${#models} -gt 120 ] && echo ' …' )"
  echo "  key 条数      : ${keycnt}"

  # channel_info 解析
  IS_MK="$(printf '%s' "$info" | sed -n 's/.*"is_multi_key":\(true\|false\).*/\1/p')"
  MK_MODE="$(printf '%s' "$info" | sed -n 's/.*"multi_key_mode":"\([^"]*\)".*/\1/p')"
  MK_SIZE="$(printf '%s' "$info" | sed -n 's/.*"multi_key_size":\([0-9]*\).*/\1/p')"
  [ -z "$IS_MK" ] && IS_MK="(未设置)"
  [ -z "$MK_MODE" ] && MK_MODE="(未设置)"
  [ -z "$MK_SIZE" ] && MK_SIZE="0"
  echo "  is_multi_key  : ${IS_MK}"
  echo "  multi_key_mode: ${MK_MODE}"
  echo "  multi_key_size: ${MK_SIZE}"

  echo "  ── 体检结论 ──"
  PROBLEMS=0

  # 1) BaseURL 不能带 /v1：中继会拼成 /v1/v1/... 直接 404
  case "$base" in
    */v1|*/v1/)
      echo "  ❌ BaseURL 末尾带了 /v1 —— 会拼成 /v1/v1/chat/completions，上游返回 404。"
      echo "     正确写法：去掉结尾的 /v1（只留域名根）。"
      PROBLEMS=1
      ;;
  esac

  # 2) models 不能是字面量 "*"：选渠道走 WHERE model = ?，没有通配
  if [ "$models" = "*" ] || [ -z "$models" ]; then
    echo "  ❌ models 是 \"*\" 或空 —— 渠道模型是精确匹配，没有通配，任何模型都匹配不到。"
    echo "     正确写法：填上游真实模型名，或把模型框留空让系统自动拉取。"
    PROBLEMS=1
  fi

  # 3) 多把 key 必须标多密钥，否则整串 key 被当成一把
  if [ "${keycnt}" -gt 1 ] && [ "$IS_MK" != "true" ]; then
    echo "  ❌ 有 ${keycnt} 把 key 但 is_multi_key=${IS_MK} —— 这就是「建的不是多key分组」。"
    echo "     后果：GetNextEnabledKey 会把整串 key 当成一把发给上游，请求全部鉴权失败。"
    PROBLEMS=1
  fi

  # 4) multi_key_mode 必须是 polling/random，空值会退化成固定取第一把
  if [ "$IS_MK" = "true" ]; then
    case "$MK_MODE" in
      polling|random) ;;
      *)
        echo "  ⚠️  multi_key_mode=\"${MK_MODE}\" 不是 polling/random —— 会走未知分支，固定取第一把 key。"
        PROBLEMS=1
        ;;
    esac
    if [ "${MK_SIZE}" != "${keycnt}" ]; then
      echo "  ⚠️  multi_key_size=${MK_SIZE} 与实际 key 条数 ${keycnt} 不一致。"
      PROBLEMS=1
    fi
  fi

  if [ "$PROBLEMS" = "0" ]; then
    echo "  ✅ 渠道结构正常。"
  else
    echo "  ↑ 以上问题会导致「渠道建了但用不了」。"
    echo "    升级到镜像 v1.0.0-rc.43-hc15 及以上后重启，启动迁移会自动修好基址、模型与多密钥标记。"
  fi
done

echo "──────────────────────────────────────────"
echo
echo "===================== 号池 Token 体检 ====================="
Q "SELECT id, name, user_id, status, IFNULL(source_channel_id,0), IFNULL(remain_quota,0) FROM tokens WHERE deleted_at IS NULL AND name LIKE '%号池%' ORDER BY id DESC LIMIT 15;"

echo
echo "===================== 号池渠道能力表 ====================="
Q "SELECT channel_id, COUNT(*) FROM abilities WHERE channel_id IN (SELECT id FROM channels WHERE tag LIKE 'keypool%') GROUP BY channel_id;"
echo "（channel_id → 能力条数；条数应等于该渠道 models 的模型个数，为 1 说明 models 还是 \"*\"）"
