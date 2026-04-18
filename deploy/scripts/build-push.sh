#!/bin/bash
# Go 项目镜像构建与上传脚本
# 用法：
#   构建本地镜像: ./deploy/scripts/build-push.sh local
#   构建 amd64镜像: ./deploy/scripts/build-push.sh amd64
#   构建并上传: ./deploy/scripts/build-push.sh push [TAG]

set -e

DOCKERHUB_USER=ceyewan
IMAGE_NAME=resonance
IMAGE_REPO=$DOCKERHUB_USER/$IMAGE_NAME
PLATFORM=linux/amd64
TAG=${2:-v0.1}

case "$1" in
  local)
    docker build \
      --target final \
      -t $IMAGE_REPO:local -f deploy/Dockerfile .
    echo "本地镜像已构建：$IMAGE_REPO:local"
    ;;
  amd64)
    docker build --platform=$PLATFORM \
      --target final \
      -t $IMAGE_REPO:amd64 -f deploy/Dockerfile .
    echo "amd64镜像已构建：$IMAGE_REPO:amd64"
    ;;
  push)
    echo "正在构建并推送镜像..."
    docker build --platform=$PLATFORM \
      --target final \
      -t $IMAGE_REPO:$TAG -f deploy/Dockerfile .
    
    docker push $IMAGE_REPO:$TAG
    echo "已上传到 Docker Hub: $IMAGE_REPO:$TAG"
    ;;
  *)
    echo "用法: $0 [local|amd64|push] [TAG]"
    exit 1
    ;;
esac
