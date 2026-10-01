#!/bin/sh
# Запускає Docker Engine всередині контейнера (для kind-кластера QA-агента),
# а потім саму програму від користувача node.
set -e

if [ "$(id -u)" = 0 ]; then
	if [ -w /sys/fs/cgroup ]; then
		# cgroup v2: перед тим як dockerd створить свої cgroup, переносимо наявні процеси
		# в окрему підгрупу і вмикаємо контролери — як це робить офіційний образ docker:dind.
		if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
			mkdir -p /sys/fs/cgroup/init
			xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs 2>/dev/null || :
			sed -e 's/ / +/g' -e 's/^/+/' </sys/fs/cgroup/cgroup.controllers \
				>/sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || :
		fi
		# Після docker restart файли контейнера лишаються, а з ними — pid-файли
		# попереднього dockerd; з ними він відмовляється стартувати.
		rm -f /var/run/docker.pid /var/run/docker/containerd/containerd.pid
		dockerd >/var/log/dockerd.log 2>&1 &
		for _ in $(seq 1 30); do
			docker info >/dev/null 2>&1 && break
			sleep 1
		done
		docker info >/dev/null 2>&1 || echo "entrypoint: dockerd не запустився, див. /var/log/dockerd.log" >&2
	else
		echo "entrypoint: контейнер не privileged — docker (і kind-кластер для QA) недоступний" >&2
	fi
	# Домашня папка і kubeconfig — для node, як і раніше.
	export HOME=/home/node USER=node
	exec setpriv --reuid=node --regid=node --init-groups "$@"
fi

exec "$@"
