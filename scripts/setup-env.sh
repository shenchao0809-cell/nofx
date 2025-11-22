#!/bin/bash
# NOFX 环境配置脚本（修复版）
# - 修复 sed: unterminated `s' command 错误
# - 兼容 Linux / macOS
# - 自动生成密钥并写入 .env

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
SECRETS_DIR="$PROJECT_ROOT/.secrets"
ENV_FILE="$PROJECT_ROOT/.env"

echo "🔐 NOFX 加密密钥配置（修复版）"
echo "===================="

# 字符串转义函数，避免 sed 注入
escape_sed() {
    echo "$1" | sed -e 's/[\/&]/\\&/g'
}

# 1. 创建 .secrets 目录
if [ ! -d "$SECRETS_DIR" ]; then
    echo "📁 创建 .secrets 目录..."
    mkdir -p "$SECRETS_DIR"
    chmod 700 "$SECRETS_DIR"
fi

# 2. 检查是否已有 .env 文件
if [ -f "$ENV_FILE" ]; then
    echo "⚠️  .env 文件已存在"
    read -p "是否覆盖? (y/N): " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "❌ 已取消操作"
        exit 0
    fi
fi

# 3. 复制 .env.example 到 .env
echo "📄 创建 .env 文件..."
cp "$PROJECT_ROOT/.env.example" "$ENV_FILE"

# 4. 生成 DATA_ENCRYPTION_KEY
echo "🔑 生成 DATA_ENCRYPTION_KEY..."
DATA_KEY=$(openssl rand -base64 32)
echo "$DATA_KEY" > "$SECRETS_DIR/master.key"
chmod 600 "$SECRETS_DIR/master.key"

SAFE_DATA_KEY=$(escape_sed "$DATA_KEY")

echo "✏️  写入 DATA_ENCRYPTION_KEY..."
if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s|DATA_ENCRYPTION_KEY=.*|DATA_ENCRYPTION_KEY=$SAFE_DATA_KEY|" "$ENV_FILE"
else
    sed -i "s|DATA_ENCRYPTION_KEY=.*|DATA_ENCRYPTION_KEY=$SAFE_DATA_KEY|" "$ENV_FILE"
fi

# 5. 生成 JWT_SECRET
echo "🔑 生成 JWT_SECRET..."
JWT_KEY=$(openssl rand -base64 64)
SAFE_JWT_KEY=$(escape_sed "$JWT_KEY")

echo "✏️  写入 JWT_SECRET..."
if [[ "$OSTYPE" == "darwin"* ]]; then
    sed -i '' "s|# JWT_SECRET=|JWT_SECRET=$SAFE_JWT_KEY|" "$ENV_FILE"
else
    sed -i "s|# JWT_SECRET=|JWT_SECRET=$SAFE_JWT_KEY|" "$ENV_FILE"
fi

echo ""
echo "✅ 配置完成！"
echo ""
echo "📋 生成的文件："
echo "   - $SECRETS_DIR/master.key"
echo "   - $ENV_FILE"
echo ""
echo "🚀 启动方式："
echo "   docker compose up -d --build"
echo ""
echo "⚠️  注意：请勿将 .env 或 .secrets 提交到 Git"
echo ""
