"""在隔离 Docker 网络中验证 Linux 管理员重启、幂等重放和 SIGTERM；不使用主机配置。"""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
PREFIX = 's02-process-' + uuid.uuid4().hex[:10]
PASSWORD = 's02-isolated-test-password'
containers = []
steps = []
started = time.monotonic()


def docker(*args, check=True):
    result = subprocess.run(['docker', *args], capture_output=True, text=True, timeout=120)
    if check and result.returncode:
        raise RuntimeError(result.stderr.replace(PASSWORD, '[test-password]'))
    return (result.stdout + (result.stderr if args[0] == 'logs' else '')).strip()


def launch(name, *args):
    containers.append(name)
    return docker('run', '-d', '--name', name, '--network', PREFIX, *args)


def http(port, path, payload=None, token=None, key=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    if key:
        headers['Idempotency-Key'] = key
    request = urllib.request.Request(f'http://127.0.0.1:{port}{path}',
                                     data=None if payload is None else json.dumps(payload).encode(), headers=headers)
    with urllib.request.urlopen(request, timeout=3) as response:
        return response.status, dict(response.headers), json.loads(response.read())


def wait_ready(port, name):
    deadline = time.monotonic() + 100
    while time.monotonic() < deadline:
        state = json.loads(docker('inspect', '--format', '{{json .State}}', name))
        if not state['Running']:
            raise RuntimeError(f'服务在就绪前退出：{state["ExitCode"]}')
        try:
            if http(port, '/health')[0] == 200:
                return
        except Exception:
            pass
        time.sleep(.1)
    raise RuntimeError('服务没有就绪')


def wait_exit(name):
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        state = json.loads(docker('inspect', '--format', '{{json .State}}', name))
        if not state['Running']:
            assert state['ExitCode'] == 0, state['ExitCode']
            return state['ExitCode']
        time.sleep(.1)
    raise RuntimeError('退出超过 HTTP 5 秒与后台 30 秒预算')


error = None
server_name = PREFIX + '-server'
try:
    docker('network', 'create', PREFIX)
    launch(PREFIX+'-pg', '--network-alias', 'postgres', '-e', 'POSTGRES_PASSWORD='+PASSWORD,
           '-e', 'POSTGRES_DB=s02_linux', 'postgres:18.1-alpine3.23')
    launch(PREFIX+'-redis', '--network-alias', 'redis', 'redis:8.4-alpine')
    for _ in range(100):
        if docker('exec', PREFIX+'-pg', 'pg_isready', '-U', 'postgres', check=False).endswith('accepting connections'):
            break
        time.sleep(.1)
    with tempfile.TemporaryDirectory(prefix=PREFIX) as data:
        Path(data, 'prices.json').write_text('{"s02-model":{"input_cost_per_token":0.000001}}')
        env = {
            'AUTO_SETUP': 'true', 'DATABASE_HOST': 'postgres', 'DATABASE_PORT': '5432',
            'DATABASE_USER': 'postgres', 'DATABASE_PASSWORD': PASSWORD, 'DATABASE_DBNAME': 's02_linux',
            'DATABASE_SSLMODE': 'disable', 'REDIS_HOST': 'redis', 'REDIS_PORT': '6379',
            'ADMIN_EMAIL': 's02-linux@example.test', 'ADMIN_PASSWORD': PASSWORD,
            'SERVER_HOST': '0.0.0.0', 'SERVER_PORT': '8080', 'TZ': 'UTC', 'DATA_DIR': '/data',
            'PRICING_REMOTE_URL': 'http://127.0.0.1:1/prices.json', 'PRICING_HASH_URL': '',
            'PRICING_DATA_DIR': '/data', 'PRICING_FALLBACK_FILE': '/data/prices.json',
        }
        args = ['--platform', 'linux/arm64', '-p', '127.0.0.1::8080', '--entrypoint', '/server',
                '-v', str(ROOT/'backend/bin/server-linux-arm64')+':/server:ro', '-v', data+':/data', '-w', '/data']
        for key, value in env.items():
            args += ['-e', key+'='+value]
        launch(server_name, *args, 'redis:8.4-alpine')
        port = int(docker('port', server_name, '8080/tcp').rsplit(':', 1)[1])
        wait_ready(port, server_name)
        status, _, login = http(port, '/api/v1/auth/login', {'email': env['ADMIN_EMAIL'], 'password': PASSWORD})
        assert status == 200
        token = login['data']['access_token']
        request_start = time.monotonic()
        first = http(port, '/api/v1/admin/system/restart', {}, token, 's02-linux-restart')
        second = http(port, '/api/v1/admin/system/restart', {}, token, 's02-linux-restart')
        assert first[0] == second[0] == 200
        assert first[2]['data']['message'] == 'Service restart initiated'
        assert first[2] == second[2]
        code = wait_exit(server_name)
        elapsed = time.monotonic() - request_start
        assert elapsed >= .5, elapsed
        steps.append({'contract': 'linux-admin-restart-and-replay', 'http_status': first[0], 'exit_code': code,
                      'seconds_after_request': round(elapsed, 3),
                      'replay_headers': {k: v for k, v in second[1].items() if 'idempoten' in k.lower()}})
        docker('start', server_name)
        port = int(docker('port', server_name, '8080/tcp').rsplit(':', 1)[1])
        wait_ready(port, server_name)
        docker('kill', '--signal=SIGTERM', server_name)
        steps.append({'contract': 'linux-sigterm-existing-install', 'exit_code': wait_exit(server_name)})
        logs = docker('logs', server_name, check=False)
        for name in ['HTTPRequests', 'DeferredService', 'TimingWheelService', 'Redis', 'Ent']:
            assert '[Lifecycle] stopped '+name in logs, name
        (HERE/'linux-process.log').write_text(logs.replace(PASSWORD, '[test-password]'))
except Exception as exc:
    error = str(exc).replace(PASSWORD, '[test-password]')
    (HERE/'linux-process.log').write_text(docker('logs', server_name, check=False).replace(PASSWORD, '[test-password]'))
finally:
    for name in reversed(containers):
        docker('rm', '-f', name, check=False)
    docker('network', 'rm', PREFIX, check=False)
    result = {'command': ['python3', 'refactor/baseline/S02/linux-process.py'], 'image': 'redis:8.4-alpine',
              'platform': 'linux/arm64', 'steps': steps, 'exit_code': 1 if error else 0,
              'duration_seconds': round(time.monotonic()-started, 3), 'error': error,
              'resources_removed': True, 'log': 'linux-process.log'}
    (HERE/'linux-process.result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps(result, ensure_ascii=False))
if error:
    raise SystemExit(1)
