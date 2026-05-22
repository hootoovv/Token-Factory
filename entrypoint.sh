#!/bin/sh
# ============================================================
# Token Factory - 容器入口脚本
# ============================================================
# 功能：以 root 身份修复挂载卷的权限，然后切换到非 root 用户运行服务
# 解决问题：Docker bind mount 挂载的目录默认归属 root:root，
#           非 root 用户 (appuser) 无法写入，导致 SQLite 数据库创建失败
# ============================================================

# 修复数据目录权限（bind mount 会覆盖镜像内的权限设置）
if [ -d "/app/data" ]; then
    chown -R appuser:appgroup /app/data
    echo "[入口] 已修复 /app/data 目录权限"
fi

# 修复配置文件权限（如果通过 bind mount 挂载）
if [ -f "/app/config.yaml" ]; then
    chown appuser:appgroup /app/config.yaml
    echo "[入口] 已修复 /app/config.yaml 文件权限"
fi

# 切换到非 root 用户执行主程序
echo "[入口] 切换到 appuser 用户启动服务..."
exec su-exec appuser ./token-factory "$@"
