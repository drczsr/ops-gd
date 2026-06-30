#!/usr/bin/env bash
# 批量合服脚本(工单系统版)
# 流程:设维护 -> 停服 -> 备份+合服 -> 清理源服 -> 更新列表 -> 启动
# 说明:相比旧版,已删除「GM热更」「全服配置热更」两步——这两件事现在由工单系统接管
#       (改库 -> 生成配置 -> 提交COS -> 全服 gd_download -> gd_gmhot),本脚本不再负责配置分发。
# 参数:$1=合服工具包.zip(位于 /export/packages/)  $2=合服列表文件(每行 "目标,源")
# 环境:SERVER_CONFIG_FILE=工单系统从配置库生成的当前 server 配置文件路径

merge_file="$1"
merge_list_file="$2"
pack_path=/export/packages/
db_file=DBMergeConfigList.txt
zone_file=MergeZoneList.txt
bak_tim=$(date +%F)
bak_path=/export/db_backup/merge_db_backup/${bak_tim}
# 以下可由工单系统(config.yaml)经 env 注入;未注入时回落到生产默认值。
SSH_PORT=${SSH_PORT:-7722}
SSH_KEY=${SSH_KEY:-/export/op/sshkey/op}
SSH_USER=${SSH_USER:-root}
HEFU_DIR=${HEFU_DIR:-/export/op/hefu}
ssh_cmd="-p ${SSH_PORT} -i ${SSH_KEY} -o StrictHostKeyChecking=no -o BatchMode=yes"
work_dir=$(pwd)

# ========== 日志 ==========
LOG_FILE="${work_dir}/merge_$(date +%Y%m%d%H%M).log"
exec > >(tee -a "$LOG_FILE") 2>&1
echo "========== 开始时间: $(date) =========="

# ========== 步骤标记(支持断点续跑) ==========
run_step() {
    local step=$1 desc=$2
    if grep -q "^${step}$" "$STEP_FILE" 2>/dev/null; then
        echo -e "\033[33m跳过已完成: ${desc}\033[0m"
        return 1
    fi
    echo -e "\033[32m========== ${desc} ==========\033[0m"
    return 0
}

done_step() {
    echo "$1" >> "$STEP_FILE"
}

fail_exit() {
    echo -e "\033[31m致命错误: $1\033[0m"
    echo "========== 失败时间: $(date) =========="
    echo -e "\033[31m修复后重新执行同样的命令，已完成的步骤会自动跳过\033[0m"
    echo -e "\033[31m步骤记录: ${STEP_FILE}\033[0m"
    exit 1
}

# ========== 参数检查 ==========
if [[ $# -ne 2 ]]; then
    echo -e "\033[31m参数个数不对(需要2个,实际${#}个)\033[0m"
    echo -e "\033[31m用法: $0 <合服工具包.zip> <合服列表文件>\033[0m"
    echo -e "\033[31m例:   $0 DBMergeTool_xxx.zip merge_list.txt\033[0m"
    exit 1
fi

[[ ! -f "${pack_path}${merge_file}" ]] && fail_exit "工具包不存在: ${pack_path}${merge_file}"

merge_list_file=$(realpath "$merge_list_file")
[[ ! -f "$merge_list_file" ]] && fail_exit "合服列表不存在: ${merge_list_file}"

# 去除Windows换行符，确保末尾有换行
sed -i 's/\r$//' "$merge_list_file"
[[ -n "$(tail -c1 "$merge_list_file")" ]] && echo "" >> "$merge_list_file"

# 步骤标记文件(基于合服列表内容的md5，重跑时不变)
_list_hash=$(md5sum "$merge_list_file" | awk '{print $1}')
STEP_FILE="/tmp/.merge_step_${_list_hash}"
echo "步骤记录文件: ${STEP_FILE}"

# ========== 预检查: 验证合服列表 ==========
declare -A _seen_source=()
while IFS=',' read -r target source; do
    [[ -z "$target" || -z "$source" ]] && continue
    [[ ! "$target" =~ ^[0-9]+$ ]] && fail_exit "目标服ID不是数字: $target"
    [[ ! "$source" =~ ^[0-9]+$ ]] && fail_exit "源服ID不是数字: $source"
    [[ "$target" == "$source" ]] && fail_exit "目标服和源服ID相同: $target"
    [[ -n "${_seen_source[$source]}" ]] && fail_exit "源服 $source 出现在多组中"
    _seen_source[$source]=1
done < "$merge_list_file"

# ========== 加载MAP(每次都加载，不跟步骤绑定) ==========
declare -A MAP=()
# 配置文件由工单系统从配置库(gameconfig)现生成并经 SERVER_CONFIG_FILE 传入;
# 不再回退到任何固定路径(工单系统不在本机维护 ServerConfigList.txt)。
[[ -z "$SERVER_CONFIG_FILE" ]] && fail_exit "未设置 SERVER_CONFIG_FILE(应由工单系统生成当前配置后传入)"
_F=$(realpath "$SERVER_CONFIG_FILE")
[[ ! -f "$_F" ]] && fail_exit "配置文件不存在: $_F"

echo -e "\033[32m加载配置: $_F\033[0m"
source <(
  tr -d '\r' < "$_F" | awk -F'\t' '
    NR==1 { for(i=1;i<=NF;i++) f[i]=$i; next }
    NR<=4 { next }
    $1!="" && $1!~/^#/ {
      for(i=1;i<=NF;i++){
        gsub(/\047/, "\047\\\047\047", $i)
        printf "MAP[\"%s:%s\"]=\047%s\047\n", $1, f[i], $i
      }
    }
  '
)
[[ ${#MAP[@]} -eq 0 ]] && fail_exit "MAP加载失败，数据为空"
echo -e "\033[32m配置加载完成, 共 ${#MAP[@]} 条\033[0m"

# 验证合服列表中的ID在MAP中存在
while IFS=',' read -r target source; do
    [[ -z "$target" || -z "$source" ]] && continue
    [[ -z "${MAP["$target:SelfPublicIp"]}" ]] && fail_exit "目标服 $target 在配置中不存在"
    [[ -z "${MAP["$source:SelfPublicIp"]}" ]] && fail_exit "源服 $source 在配置中不存在"
done < "$merge_list_file"

# ========== 第1步: 设为维护状态 ==========
if run_step 1 "第1步: 设为维护状态"; then
    all_ids=""
    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue
        [[ -n "$all_ids" ]] && all_ids="${all_ids},"
        all_ids="${all_ids}${target},${source}"
    done < "$merge_list_file"

    [[ -z "$all_ids" ]] && fail_exit "维护ID为空，请检查合服列表"
    echo -e "\033[32m维护ID: ${all_ids}\033[0m"
    cd "${HEFU_DIR}" && ./update_serverlist_stat.sh 0 "${all_ids}" > /tmp/step1.log 2>&1
    if [[ $? -ne 0 ]]; then
        cat /tmp/step1.log; rm -f /tmp/step1.log
        fail_exit "设置维护状态失败"
    fi
    rm -f /tmp/step1.log
    cd "$work_dir"
    echo -e "\033[32m维护状态设置完成\033[0m"
    done_step 1
fi

# ========== 第2步: 停服(先停战斗服+副本服，再停游戏服) ==========
if run_step 2 "第2步: 停服"; then
    declare -A STOPPED=()
    declare -A BATTLE_IPS=()
    declare -A BATTLE_PIDS=()
    declare -A GAME_IPS=()
    declare -A GAME_PIDS=()

    # 收集需要停的服务器(去重，分两组)
    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue

        battle_id="${MAP["$target:BattleWorldID"]}"
        copy_id=$(tr -d '\r' < "$_F" | awk -F'\t' -v bid="$battle_id" '
          NR==1{for(i=1;i<=NF;i++){if($i=="BattleWorldID")bc=i;if($i=="WorldType")wc=i}}
          NR>4 && $bc==bid && $wc==3{print $1;exit}')

        # 游戏服
        for id in $target $source; do
            if [[ -n "$id" && "$id" != "-1" && -z "${STOPPED[$id]}" ]]; then
                ip="${MAP["$id:SelfPublicIp"]}"
                [[ -z "$ip" ]] && fail_exit "服务器 $id 找不到IP"
                STOPPED[$id]=1
                GAME_IPS[$id]="$ip"
            fi
        done

        # 战斗服+副本服
        for id in $battle_id $copy_id; do
            if [[ -n "$id" && "$id" != "-1" && -z "${STOPPED[$id]}" ]]; then
                ip="${MAP["$id:SelfPublicIp"]}"
                [[ -z "$ip" ]] && fail_exit "服务器 $id 找不到IP"
                STOPPED[$id]=1
                BATTLE_IPS[$id]="$ip"
            fi
        done
    done < "$merge_list_file"

    # 并行停服(输出重定向到临时文件，失败时才显示)
    echo -e "\033[32m--- 停战斗服+副本服 ---\033[0m"
    for id in "${!BATTLE_IPS[@]}"; do
        ip="${BATTLE_IPS[$id]}"
        echo -e "\033[32m停服: $id @ $ip\033[0m"
        ssh -n $ssh_cmd ${SSH_USER}@${ip} "cd /export/server/server_${id}/OperationalTools && ./gameserver_stop.py -worldid=${id}" > /tmp/stop_${id}.log 2>&1 &
        BATTLE_PIDS[$id]=$!
    done

    echo -e "\033[33m正在停服中(踢人+关闭各组件，约需30秒+)，请稍候...\033[0m"
    stop_fail=0
    for id in "${!BATTLE_PIDS[@]}"; do
        wait ${BATTLE_PIDS[$id]}
        ret=$?
        if [[ $ret -ne 0 && $ret -ne 104 ]]; then
            echo -e "\033[31m停服失败(exit=$ret): $id @ ${BATTLE_IPS[$id]}\033[0m"
            cat /tmp/stop_${id}.log
            stop_fail=1
        else
            echo -e "\033[32m停服完成: $id\033[0m"
        fi
        rm -f /tmp/stop_${id}.log
    done
    [[ $stop_fail -eq 1 ]] && fail_exit "部分战斗服/副本服停服失败，请检查上方日志"

    # 第二波：并行停游戏服
    echo -e "\033[32m--- 停游戏服 ---\033[0m"
    for id in "${!GAME_IPS[@]}"; do
        ip="${GAME_IPS[$id]}"
        echo -e "\033[32m停服: $id @ $ip\033[0m"
        ssh -n $ssh_cmd ${SSH_USER}@${ip} "cd /export/server/server_${id}/OperationalTools && ./gameserver_stop.py -worldid=${id}" > /tmp/stop_${id}.log 2>&1 &
        GAME_PIDS[$id]=$!
    done

    echo -e "\033[33m正在停服中(踢人+关闭各组件，约需30秒+)，请稍候...\033[0m"
    stop_fail=0
    for id in "${!GAME_PIDS[@]}"; do
        wait ${GAME_PIDS[$id]}
        ret=$?
        if [[ $ret -ne 0 && $ret -ne 104 ]]; then
            echo -e "\033[31m停服失败(exit=$ret): $id @ ${GAME_IPS[$id]}\033[0m"
            cat /tmp/stop_${id}.log
            stop_fail=1
        else
            echo -e "\033[32m停服完成: $id\033[0m"
        fi
        rm -f /tmp/stop_${id}.log
    done
    [[ $stop_fail -eq 1 ]] && fail_exit "部分游戏服停服失败，请检查上方日志"

    done_step 2
fi

# ========== 第3步: 备份+合服(按组记录进度) ==========
if run_step 3 "第3步: 备份+合服"; then
    # 检查备份目录磁盘空间(至少10G)
    [[ ! -d ${bak_path} ]] && mkdir -p ${bak_path}
    avail_kb=$(df -kP "${bak_path}" | awk 'NR==2{print $4}')
    if [[ $avail_kb -lt 10485760 ]]; then
        fail_exit "备份目录磁盘空间不足: $(( avail_kb / 1024 / 1024 ))G 可用，需要至少10G"
    fi

    cd "$work_dir"
    cp "${pack_path}${merge_file}" .
    merge_path=$(echo "${merge_file}" | awk -F'.' '{print $1}')
    [[ ! -d "${merge_path}" ]] && unzip -oq "${merge_file}"
    cd "${merge_path}" || fail_exit "进入合服工具目录失败: ${merge_path}"
    [[ ! -f Config/${db_file} ]] && fail_exit "合服配置文件不存在: ${merge_path}/Config/${db_file}"

    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue

        # 按组跳过已完成的
        if grep -q "^merge_${target}_${source}$" "$STEP_FILE" 2>/dev/null; then
            echo -e "\033[33m跳过已完成: 合服 $target <- $source\033[0m"
            continue
        fi

        echo -e "\033[32m========== 合服: $target <- $source ==========\033[0m"

        sed -i '1,/^#/!d' Config/${db_file}
        echo "" > Config/${zone_file}

        for id in $target $source; do
            dbip="${MAP["$id:MySqlIp"]}"
            dbport="${MAP["$id:MySqlPort"]}"
            dbuser="${MAP["$id:DataBaseUser"]}"
            dbpasswd="${MAP["$id:DataBasePsw"]}"

            [[ -z "$dbip" || -z "$dbport" ]] && fail_exit "服务器 $id 找不到数据库信息"

            # 先测试连接，再检查DB
            if ! mysql -h"${dbip}" -u"${dbuser}" -p"${dbpasswd}" -P"${dbport}" -Ne "SELECT 1" &>/dev/null; then
                fail_exit "数据库连接失败: ${dbip}:${dbport} (用户:${dbuser})"
            fi
            check_db=$(mysql -h"${dbip}" -u"${dbuser}" -p"${dbpasswd}" -P"${dbport}" -Ne "show databases like 'ptdb_${id}'" 2>/dev/null | wc -l)
            [[ ${check_db} -eq 0 ]] && fail_exit "数据库 ptdb_${id} 不存在 (dbip=${dbip})"

            printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$id" "$dbip" "$dbport" "$dbuser" "$dbpasswd" "ptdb_${id}" "-1" "$id" >> Config/${db_file}

            if [[ ! -f ${bak_path}/ptdb_${id}_dump.sql.gz ]]; then
                echo -e "\033[32m备份: ptdb_${id}\033[0m"
                mysqldump --set-gtid-purged=OFF --routines -h"${dbip}" -u"${dbuser}" -p"${dbpasswd}" -P"${dbport}" ptdb_${id} 2>/tmp/mysqldump_err_$$.log | gzip > "${bak_path}/ptdb_${id}_dump.sql.gz"
                dump_exit=${PIPESTATUS[0]}
                # 检查mysqldump退出码和错误日志
                if [[ $dump_exit -ne 0 ]] || grep -qi "error" /tmp/mysqldump_err_$$.log 2>/dev/null; then
                    rm -f "${bak_path}/ptdb_${id}_dump.sql.gz"
                    fail_exit "备份 ptdb_${id} 失败(exit=$dump_exit): $(cat /tmp/mysqldump_err_$$.log 2>/dev/null)"
                fi
                bak_size=$(stat -c%s "${bak_path}/ptdb_${id}_dump.sql.gz" 2>/dev/null || echo 0)
                if [[ $bak_size -lt 1024 ]]; then
                    rm -f "${bak_path}/ptdb_${id}_dump.sql.gz"
                    fail_exit "备份 ptdb_${id} 失败，文件过小(${bak_size}字节)"
                fi
                rm -f /tmp/mysqldump_err_$$.log
            else
                echo -e "\033[32mptdb_${id} 备份已存在,跳过\033[0m"
            fi
        done

        echo -e "${target}\t${source}" > Config/${zone_file}

        python DBMergeScript/dbmerge_run.py -tarworldid=${target} > /tmp/merge_${target}_${source}.log 2>&1
        if [[ $? -ne 0 ]]; then
            grep -v '\[Warning\] Using a password' /tmp/merge_${target}_${source}.log
            rm -f /tmp/merge_${target}_${source}.log
            fail_exit "合服执行失败: $target <- $source"
        fi
        rm -f /tmp/merge_${target}_${source}.log
        echo -e "\033[32m合服完成: $target <- $source\033[0m"

        # 记录本组完成
        done_step "merge_${target}_${source}"

    done < "$merge_list_file"
    cd "$work_dir"
    done_step 3
fi

# ========== 第4步: 清理源服(必须前3步全部完成) ==========
for s in 1 2 3; do
    grep -q "^${s}$" "$STEP_FILE" 2>/dev/null || fail_exit "第${s}步未完成，不能执行清理源服"
done

if run_step 4 "第4步: 清理源服"; then
    cd "$work_dir"
    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue

        # 按源服跳过已清理的
        if grep -q "^clean_${source}$" "$STEP_FILE" 2>/dev/null; then
            echo -e "\033[33m跳过已清理: ${source}\033[0m"
            continue
        fi

        echo -e "\033[32m========== 清理源服: $source ==========\033[0m"
        ip="${MAP["$source:SelfPublicIp"]}"

        cd "${HEFU_DIR}" && ./del_alb_rule.sh s${source} > /tmp/clean_${source}.log 2>&1
        if [[ $? -ne 0 ]]; then
            cat /tmp/clean_${source}.log; rm -f /tmp/clean_${source}.log
            fail_exit "删除ALB失败: s${source}"
        fi
        echo -e "\033[32m  删除ALB规则完成\033[0m"

        ssh -n $ssh_cmd ${SSH_USER}@${ip} "cd /export/scripts && ./del_server.sh ${source}" > /tmp/clean_${source}.log 2>&1
        if [[ $? -ne 0 ]]; then
            cat /tmp/clean_${source}.log; rm -f /tmp/clean_${source}.log
            fail_exit "删除server目录失败: ${source} @ ${ip}"
        fi
        echo -e "\033[32m  删除server目录完成\033[0m"

        cd "${HEFU_DIR}" && ./auth_user.sh 0 ${source} > /tmp/clean_${source}.log 2>&1
        if [[ $? -ne 0 ]]; then
            cat /tmp/clean_${source}.log; rm -f /tmp/clean_${source}.log
            fail_exit "删除运维后台账号失败: ${source}"
        fi
        echo -e "\033[32m  删除运维后台账号完成\033[0m"

        cd "${HEFU_DIR}" && ./del_serverlist.sh ${source} > /tmp/clean_${source}.log 2>&1
        if [[ $? -ne 0 ]]; then
            cat /tmp/clean_${source}.log; rm -f /tmp/clean_${source}.log
            fail_exit "删除合服列表失败: ${source}"
        fi
        echo -e "\033[32m  删除运维后台合服列表完成\033[0m"
        rm -f /tmp/clean_${source}.log

        cd "$work_dir"
        done_step "clean_${source}"
    done < "$merge_list_file"
    done_step 4
fi

# ========== 第5步: 更新服务器列表 ==========
if run_step 5 "第5步: 更新服务器列表"; then
    merge_args=()
    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue
        merge_args+=("${target},${source}")
    done < "$merge_list_file"

    [[ ${#merge_args[@]} -eq 0 ]] && fail_exit "更新列表为空，请检查合服列表"
    echo -e "\033[32m更新列表: ${merge_args[*]}\033[0m"
    cd "${HEFU_DIR}" && ./hefu_update_serverlist.sh "${merge_args[@]}" > /tmp/step5.log 2>&1
    if [[ $? -ne 0 ]]; then
        cat /tmp/step5.log; rm -f /tmp/step5.log
        fail_exit "更新服务器列表失败"
    fi
    rm -f /tmp/step5.log
    cd "$work_dir"
    echo -e "\033[32m服务器列表更新完成\033[0m"
    done_step 5
fi

# ========== 第6步: 启动服务器(并行) ==========
if run_step 6 "第6步: 启动服务器"; then
    declare -A STARTED=()
    declare -A START_PIDS=()
    declare -A START_IPS=()

    # 收集需要启动的服务器(去重)
    while IFS=',' read -r target source; do
        [[ -z "$target" || -z "$source" ]] && continue

        battle_id="${MAP["$target:BattleWorldID"]}"
        copy_id=$(tr -d '\r' < "$_F" | awk -F'\t' -v bid="$battle_id" '
          NR==1{for(i=1;i<=NF;i++){if($i=="BattleWorldID")bc=i;if($i=="WorldType")wc=i}}
          NR>4 && $bc==bid && $wc==3{print $1;exit}')

        for id in $target $battle_id $copy_id; do
            if [[ -n "$id" && "$id" != "-1" && -z "${STARTED[$id]}" ]]; then
                ip="${MAP["$id:SelfPublicIp"]}"
                [[ -z "$ip" ]] && fail_exit "服务器 $id 找不到IP"
                STARTED[$id]=1
                START_IPS[$id]="$ip"
            fi
        done
    done < "$merge_list_file"

    # 并行启动
    for id in "${!START_IPS[@]}"; do
        ip="${START_IPS[$id]}"
        echo -e "\033[32m启动: $id @ $ip\033[0m"
        ssh -n $ssh_cmd ${SSH_USER}@${ip} "cd /export/server/server_${id}/OperationalTools && ./gameserver_start.py -worldid=${id} > /tmp/gameserver_start_${id}.log 2>&1" &
        START_PIDS[$id]=$!
    done

    # 等待所有启动完成，检查结果
    start_fail=0
    for id in "${!START_PIDS[@]}"; do
        wait ${START_PIDS[$id]}
        ret=$?
        if [[ $ret -ne 0 ]]; then
            echo -e "\033[31m启动失败(exit=$ret): $id @ ${START_IPS[$id]}\033[0m"
            # 尝试获取远程启动日志
            ssh -n $ssh_cmd ${SSH_USER}@${START_IPS[$id]} "cat /tmp/gameserver_start_${id}.log 2>/dev/null" 2>/dev/null
            start_fail=1
        else
            echo -e "\033[32m启动完成: $id\033[0m"
        fi
    done
    [[ $start_fail -eq 1 ]] && fail_exit "部分服务器启动失败，请检查上方日志"

    done_step 6
fi

# ========== (放开登录/恢复目标服正常状态:由工单系统"开放"阶段处理,本脚本不做) ==========

echo "========== 全部完成: $(date) =========="
echo -e "\033[32m日志文件: ${LOG_FILE}\033[0m"
echo -e "\033[32m步骤记录: ${STEP_FILE} (删除此文件可重新执行全部步骤)\033[0m"
