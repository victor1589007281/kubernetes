#!/bin/bash
#===============================================================================
# 脚本名称: 05-setup-registry.sh
# 脚本描述: 部署私有Docker镜像仓库 (Harbor)
# 作者: Auto-generated
# 版本: 1.0
#===============================================================================

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step() { echo -e "${BLUE}[STEP]${NC} $1"; }

# 检查root权限
if [[ $EUID -ne 0 ]]; then
    log_error "此脚本需要root权限运行"
    exit 1
fi

# 配置
HARBOR_VERSION="2.11.2"
REGISTRY_HOSTNAME=${1:-"registry.local"}
REGISTRY_IP=$(hostname -I | awk '{print $1}')
HARBOR_DATA_DIR="/data/harbor"
HARBOR_INSTALL_DIR="/opt/harbor"

log_info "Harbor版本: v${HARBOR_VERSION}"
log_info "Registry主机名: ${REGISTRY_HOSTNAME}"
log_info "Registry IP: ${REGISTRY_IP}"

#===============================================================================
# 1. 安装Docker (Harbor需要)
#===============================================================================
log_step "1. 安装Docker..."

# 如果没有安装Docker，则安装
if ! command -v docker &> /dev/null; then
    # 添加Docker GPG密钥
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg

    # 添加Docker仓库
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
        > /etc/apt/sources.list.d/docker.list

    apt-get update
    apt-get install -y docker-ce docker-ce-cli docker-compose-plugin

    # 启动Docker服务
    systemctl enable docker
    systemctl start docker
    
    log_info "Docker安装完成: $(docker --version)"
else
    log_info "Docker已安装: $(docker --version)"
fi

#===============================================================================
# 2. 创建自签名证书
#===============================================================================
log_step "2. 创建自签名SSL证书..."

mkdir -p /etc/harbor/ssl
cd /etc/harbor/ssl

# 创建CA私钥
openssl genrsa -out ca.key 4096

# 创建CA证书
openssl req -x509 -new -nodes -sha512 -days 3650 \
    -subj "/C=CN/ST=Beijing/L=Beijing/O=K8S/OU=DevOps/CN=${REGISTRY_HOSTNAME}" \
    -key ca.key \
    -out ca.crt

# 创建服务器私钥
openssl genrsa -out ${REGISTRY_HOSTNAME}.key 4096

# 创建证书签名请求
openssl req -sha512 -new \
    -subj "/C=CN/ST=Beijing/L=Beijing/O=K8S/OU=DevOps/CN=${REGISTRY_HOSTNAME}" \
    -key ${REGISTRY_HOSTNAME}.key \
    -out ${REGISTRY_HOSTNAME}.csr

# 创建x509 v3扩展文件
cat > v3.ext << EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:FALSE
keyUsage = digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[alt_names]
DNS.1=${REGISTRY_HOSTNAME}
DNS.2=localhost
IP.1=${REGISTRY_IP}
IP.2=127.0.0.1
EOF

# 签发服务器证书
openssl x509 -req -sha512 -days 3650 \
    -extfile v3.ext \
    -CA ca.crt -CAkey ca.key -CAcreateserial \
    -in ${REGISTRY_HOSTNAME}.csr \
    -out ${REGISTRY_HOSTNAME}.crt

log_info "SSL证书创建完成"

#===============================================================================
# 3. 配置Docker信任证书
#===============================================================================
log_step "3. 配置Docker信任证书..."

# 为Docker配置证书
mkdir -p /etc/docker/certs.d/${REGISTRY_HOSTNAME}
cp ${REGISTRY_HOSTNAME}.crt /etc/docker/certs.d/${REGISTRY_HOSTNAME}/
cp ${REGISTRY_HOSTNAME}.key /etc/docker/certs.d/${REGISTRY_HOSTNAME}/
cp ca.crt /etc/docker/certs.d/${REGISTRY_HOSTNAME}/

# 转换证书格式为.cert (Docker使用)
openssl x509 -inform PEM -in ${REGISTRY_HOSTNAME}.crt \
    -out /etc/docker/certs.d/${REGISTRY_HOSTNAME}/${REGISTRY_HOSTNAME}.cert

# 将CA添加到系统信任
cp ca.crt /usr/local/share/ca-certificates/${REGISTRY_HOSTNAME}.crt
update-ca-certificates

# 重启Docker
systemctl restart docker

log_info "Docker证书配置完成"

#===============================================================================
# 4. 下载和安装Harbor
#===============================================================================
log_step "4. 下载Harbor..."

cd /tmp
wget -q "https://github.com/goharbor/harbor/releases/download/v${HARBOR_VERSION}/harbor-offline-installer-v${HARBOR_VERSION}.tgz" \
    -O harbor-offline-installer.tgz

tar -xzf harbor-offline-installer.tgz
mv harbor ${HARBOR_INSTALL_DIR}
rm -f harbor-offline-installer.tgz

log_info "Harbor下载完成"

#===============================================================================
# 5. 配置Harbor
#===============================================================================
log_step "5. 配置Harbor..."

cd ${HARBOR_INSTALL_DIR}

# 备份默认配置
cp harbor.yml.tmpl harbor.yml

# 修改配置
sed -i "s|hostname: reg.mydomain.com|hostname: ${REGISTRY_HOSTNAME}|g" harbor.yml
sed -i "s|certificate: /your/certificate/path|certificate: /etc/harbor/ssl/${REGISTRY_HOSTNAME}.crt|g" harbor.yml
sed -i "s|private_key: /your/private/key/path|private_key: /etc/harbor/ssl/${REGISTRY_HOSTNAME}.key|g" harbor.yml
sed -i "s|harbor_admin_password: Harbor12345|harbor_admin_password: Admin@123456|g" harbor.yml

# 配置数据目录
mkdir -p ${HARBOR_DATA_DIR}
sed -i "s|data_volume: /data|data_volume: ${HARBOR_DATA_DIR}|g" harbor.yml

log_info "Harbor配置文件已更新"

#===============================================================================
# 6. 安装Harbor
#===============================================================================
log_step "6. 安装Harbor..."

# 执行安装脚本
./install.sh --with-trivy

log_info "Harbor安装完成"

#===============================================================================
# 7. 配置Harbor开机启动
#===============================================================================
log_step "7. 配置Harbor开机启动..."

cat > /etc/systemd/system/harbor.service << EOF
[Unit]
Description=Harbor Container Registry
After=docker.service
Requires=docker.service

[Service]
Type=simple
Restart=on-failure
RestartSec=5
ExecStart=/usr/bin/docker compose -f ${HARBOR_INSTALL_DIR}/docker-compose.yml up
ExecStop=/usr/bin/docker compose -f ${HARBOR_INSTALL_DIR}/docker-compose.yml down

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable harbor

log_info "Harbor开机启动配置完成"

#===============================================================================
# 8. 更新hosts文件
#===============================================================================
log_step "8. 更新hosts文件..."

# 添加registry到hosts
if ! grep -q "${REGISTRY_HOSTNAME}" /etc/hosts; then
    echo "${REGISTRY_IP} ${REGISTRY_HOSTNAME}" >> /etc/hosts
fi

log_info "hosts文件已更新"

#===============================================================================
# 9. 配置containerd信任镜像仓库
#===============================================================================
log_step "9. 配置containerd信任镜像仓库..."

# 为containerd配置私有仓库
mkdir -p /etc/containerd/certs.d/${REGISTRY_HOSTNAME}

cat > /etc/containerd/certs.d/${REGISTRY_HOSTNAME}/hosts.toml << EOF
server = "https://${REGISTRY_HOSTNAME}"

[host."https://${REGISTRY_HOSTNAME}"]
  ca = "/etc/harbor/ssl/ca.crt"
  skip_verify = false
EOF

# 更新containerd配置以支持registry config
if ! grep -q "config_path" /etc/containerd/config.toml; then
    sed -i '/\[plugins."io.containerd.grpc.v1.cri".registry\]/a\      config_path = "/etc/containerd/certs.d"' /etc/containerd/config.toml
fi

# 重启containerd
systemctl restart containerd

log_info "containerd配置完成"

#===============================================================================
# 10. 创建Kubernetes Secret
#===============================================================================
log_step "10. 为Kubernetes创建镜像拉取Secret..."

# 等待Harbor启动
log_info "等待Harbor服务启动..."
sleep 30

# 创建docker-registry secret
kubectl create secret docker-registry harbor-registry \
    --docker-server=https://${REGISTRY_HOSTNAME} \
    --docker-username=admin \
    --docker-password=Admin@123456 \
    --docker-email=admin@local \
    -n default --dry-run=client -o yaml | kubectl apply -f -

# 在kube-system命名空间也创建
kubectl create secret docker-registry harbor-registry \
    --docker-server=https://${REGISTRY_HOSTNAME} \
    --docker-username=admin \
    --docker-password=Admin@123456 \
    --docker-email=admin@local \
    -n kube-system --dry-run=client -o yaml | kubectl apply -f -

log_info "Kubernetes镜像拉取Secret创建完成"

#===============================================================================
# 验证步骤
#===============================================================================
log_step "开始验证Harbor安装..."

VERIFY_PASSED=0
VERIFY_FAILED=0

verify_check() {
    local name=$1
    local cmd=$2
    if eval "$cmd" &>/dev/null; then
        log_info "✓ $name"
        ((VERIFY_PASSED++))
    else
        log_error "✗ $name"
        ((VERIFY_FAILED++))
    fi
}

# 等待Harbor完全启动
log_info "等待Harbor服务完全启动..."
for i in {1..60}; do
    if curl -ks https://${REGISTRY_HOSTNAME}/api/v2.0/health | grep -q "healthy"; then
        log_info "Harbor API健康检查通过"
        break
    fi
    sleep 5
done

echo ""
log_step "================== 验证结果 =================="

# 验证Docker
verify_check "Docker服务运行中" "systemctl is-active docker"

# 验证Harbor容器
verify_check "Harbor Core运行中" "docker ps | grep -q harbor-core"
verify_check "Harbor Registry运行中" "docker ps | grep -q registry"
verify_check "Harbor Portal运行中" "docker ps | grep -q harbor-portal"
verify_check "Harbor Database运行中" "docker ps | grep -q harbor-db"

# 验证API
verify_check "Harbor API可访问" "curl -ks https://${REGISTRY_HOSTNAME}/api/v2.0/health | grep -q healthy"

# 验证Docker登录
verify_check "Docker可以登录到Harbor" "docker login ${REGISTRY_HOSTNAME} -u admin -p Admin@123456"

# 验证证书
verify_check "SSL证书有效" "openssl s_client -connect ${REGISTRY_HOSTNAME}:443 </dev/null 2>/dev/null | grep -q 'Verify return code: 0'"

echo ""
log_step "==============================================="
log_info "验证通过: ${VERIFY_PASSED} 项"
if [[ $VERIFY_FAILED -gt 0 ]]; then
    log_warn "验证失败: ${VERIFY_FAILED} 项"
fi

# 显示Harbor信息
echo ""
log_step "================== Harbor信息 =================="
echo ""
echo "Harbor URL: https://${REGISTRY_HOSTNAME}"
echo "管理员用户: admin"
echo "管理员密码: Admin@123456"
echo "数据目录: ${HARBOR_DATA_DIR}"
echo ""
echo "=== Harbor容器状态 ==="
docker ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}" | grep harbor
echo ""

# 测试推送镜像
log_step "11. 测试推送镜像到Harbor..."

# 拉取一个测试镜像
docker pull busybox:latest

# 标记镜像
docker tag busybox:latest ${REGISTRY_HOSTNAME}/library/busybox:latest

# 推送镜像
if docker push ${REGISTRY_HOSTNAME}/library/busybox:latest; then
    log_info "✓ 镜像推送测试成功"
else
    log_warn "✗ 镜像推送测试失败"
fi

log_info "私有镜像仓库部署完成！"

echo ""
echo "=========================================="
echo "      Harbor镜像仓库安装完成!"
echo "      URL: https://${REGISTRY_HOSTNAME}"
echo "      用户: admin / Admin@123456"
echo "=========================================="

# 保存配置信息
cat > /root/harbor-info.txt << EOF
Harbor私有镜像仓库信息
======================
URL: https://${REGISTRY_HOSTNAME}
IP: ${REGISTRY_IP}
用户名: admin
密码: Admin@123456
数据目录: ${HARBOR_DATA_DIR}
安装目录: ${HARBOR_INSTALL_DIR}
SSL证书: /etc/harbor/ssl/

使用示例:
---------
# Docker登录
docker login ${REGISTRY_HOSTNAME} -u admin -p Admin@123456

# 推送镜像
docker tag myimage:tag ${REGISTRY_HOSTNAME}/library/myimage:tag
docker push ${REGISTRY_HOSTNAME}/library/myimage:tag

# Kubernetes使用
imagePullSecrets:
  - name: harbor-registry
EOF

log_info "配置信息已保存到: /root/harbor-info.txt"
