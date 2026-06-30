#!/bin/bash

sid=$1
cd /export/server/server_${sid}/Config/
wget -qN https://your-bucket.cos.your-region.myqcloud.com/server/ServerConfigList.txt https://your-bucket.cos.your-region.myqcloud.com/server/MergeServerFunction.txt
if [ $? -eq 0 ]; then
    echo "sid_${sid}: 配置文件更新成功！"
    exit 0
else
    echo "错误: 配置文件更新失败！"
    exit 1
fi 