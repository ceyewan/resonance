#!/bin/bash
# Go 项目镜像构建与上传脚本
# 用法：
#   构建本地镜像: ./deploy/scripts/build-push.sh local [CGO_ENABLED]
#   构建 amd64镜像: ./deploy/scripts/build-push.sh amd64 [CGO_ENABLED]
#   构建并上传: ./deploy/scripts/build-push.sh push [CGO_ENABLED] [TAG]

set -e

DOCKERHUB_USER=ceyewan
IMAGE_NAME=resonance
PLATFORM=linux/amd64
CGO_ENABLED=${2:-0}
TAG=${3:-v0.1}

case "$1" in
  local)
    docker build \
      --build-arg CGO_ENABLED=$CGO_ENABLED \
      --target $( [ "$CGO_ENABLED" = "1" ] && echo final-cgo || echo final ) \
      -t $IMAGE_NAME:local -f deploy/Dockerfile .
    echo "本地镜像已构建：$IMAGE_NAME:local (CGO_ENABLED=$CGO_ENABLED)"
    ;;
  amd64)
    docker build --platform=$PLATFORM \
      --build-arg CGO_ENABLED=$CGO_ENABLED \
      --target $( [ "$CGO_ENABLED" = "1" ] && echo final-cgo || echo final ) \
      -t $IMAGE_NAME:amd64 -f deploy/Dockerfile .
    echo "amd64镜像已构建：$IMAGE_NAME:amd64 (CGO_ENABLED=$CGO_ENABLED)"
    ;;
  push)
    echo "正在构建并推送镜像..."
    docker build --platform=$PLATFORM \
      --build-arg CGO_ENABLED=$CGO_ENABLED \
      --target $( [ "$CGO_ENABLED" = "1" ] && echo final-cgo || echo final ) \
      -t $DOCKERHUB_USER/$IMAGE_NAME:$TAG -f deploy/Dockerfile .
    
    docker push $DOCKERHUB_USER/$IMAGE_NAME:$TAG
    echo "已上传到 Docker Hub: $DOCKERHUB_USER/$IMAGE_NAME:$TAG"
    ;;
  *)
    echo "用法: $0 [local|amd64|push] [CGO_ENABLED] [TAG]"
    exit 1
    ;;
esac
