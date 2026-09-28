#!/usr/bin/env python3
"""Exercise a candidate plugin in its pinned Linux runtime using disposable data."""

import argparse
import hashlib
import http.client
import io
import json
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
import uuid


def docker(*args, data=None):
    return subprocess.run(
        ['docker', *args], input=data, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=True, timeout=120,
    ).stdout


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plugin', type=Path, required=True)
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    plugin = args.plugin.resolve(strict=True)
    release = json.loads((root / 'plugin-abi-release.json').read_text())
    runtime = release['runtime']
    assert re.fullmatch(r'ghcr.io/killbus/rulego-server@sha256:[0-9a-f]{64}', runtime)
    digest = hashlib.sha256(plugin.read_bytes()).hexdigest()
    sidecar = json.loads(Path(str(plugin) + '.abi.json').read_text())
    assert sidecar['plugin_sha256'] == digest
    assert sidecar['abi_id'] == release['abi_id']
    assert sidecar['lock_digest'] == release['lock_digest']
    platform = sidecar['platform']
    assert platform in ('linux/amd64', 'linux/arm64')
    docker('version')
    (root / 'tmp').mkdir(exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='origin-http-', dir=root / 'tmp'))
    work.chmod(0o755)
    data_dir = work / 'data'
    (data_dir / 'plugins').mkdir(parents=True)
    data_dir.chmod(0o777)
    shutil.copyfile(plugin, data_dir / 'plugins' / 'origin.so')
    config = work / 'config.conf'
    config.write_text(
        'data_dir = ./data\nserver = :9090\ndefault_username = admin\n'
        'node_pool_file = ./node_pool.json\nshare_http_server = true\n'
        'resource_mapping = /resources/=./data/resource-origin/ready/\n'
        'read_timeout = 30\nwrite_timeout = 30\n', encoding='utf-8',
    )
    owner = {
        'ruleChain': {'id': 'origin-test-pool', 'name': 'Disposable origin owner'},
        'metadata': {'nodes': [{
            'id': 'resource-origin', 'type': 'resourceOrigin', 'configuration': {
                'root': './data/resource-origin', 'staticUrlPrefix': '/resources',
                'maxRetainedBytes': 1048576, 'maxResourceBytes': 65536,
                'maxTtlMs': 600000, 'maxProductionMs': 60000,
            },
        }]},
    }
    pool = work / 'node_pool.json'
    pool.write_text(json.dumps(owner), encoding='utf-8')
    chain = {
        'ruleChain': {'id': 'origin-http-test', 'name': 'Origin HTTP contract', 'root': True},
        'metadata': {
            'firstNodeIndex': 0,
            'endpoints': [{
                'id': 'http', 'type': 'endpoint/http',
                'configuration': {'server': 'ref://:9090'},
                'routers': [{
                    'id': 'operation', 'params': ['POST'],
                    'from': {'path': '/origin-test'},
                    'to': {'path': 'origin-http-test:origin', 'wait': True,
                           'processors': ['resourceOriginResponse']},
                }],
            }],
            'nodes': [
                {'id': 'origin', 'type': 'resourceOrigin',
                 'configuration': {'root': 'ref://resource-origin'}},
                {'id': 'end', 'type': 'end'},
            ],
            'connections': [
                {'fromId': 'origin', 'toId': 'end', 'type': relation}
                for relation in ('Produce', 'Success', 'Failure')
            ],
        },
    }
    name = 'origin-http-' + uuid.uuid4().hex[:12]
    created = False
    port = None

    def request(method, path, body=None, headers=None):
        connection = http.client.HTTPConnection('127.0.0.1', port, timeout=10)
        try:
            headers = dict(headers or {})
            if body is not None:
                body = json.dumps(body).encode()
                headers['Content-Type'] = 'application/json'
            connection.request(method, path, body, headers)
            response = connection.getresponse()
            return response.status, dict((k.lower(), v) for k, v in response.getheaders()), response.read()
        finally:
            connection.close()

    def wait_ready():
        nonlocal port
        bindings = json.loads(docker('inspect', name))[0]['NetworkSettings']['Ports']
        port = int(bindings['9090/tcp'][0]['HostPort'])
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            try:
                status, _, body = request('GET', '/api/v1/components')
                if status == 200:
                    components = json.loads(body)
                    assert any(n['type'] == 'resourceOrigin' for n in components['nodes'])
                    return
            except (OSError, http.client.HTTPException):
                pass
            time.sleep(0.2)
        raise AssertionError('runtime did not become ready')

    def operation(expected, **body):
        status, headers, payload = request('POST', '/origin-test', body)
        assert status == expected, (status, expected, payload)
        return json.loads(payload), headers

    payload = b'origin-http-contract-' * 128

    def publish(key, ttl, parent=''):
        lease, _ = operation(202, operation='acquire', key=key, fingerprint='fixture-v1',
                             ttlMs=ttl, maxBytes=65536, productionTimeoutMs=60000,
                             parentResourceId=parent)
        assert re.fullmatch(r'[0-9a-f]{64}', lease['resourceId'])
        assert re.fullmatch(r'[0-9a-f]{32}', lease['generation'])
        staging = '/app/data/resource-origin/staging/' + lease['resourceId'] + '/' + lease['generation']
        assert lease['stagingDir'] == staging
        archive = io.BytesIO()
        with tarfile.open(fileobj=archive, mode='w') as tar:
            member = tarfile.TarInfo('artifact.ts')
            member.uid = member.gid = 65532
            member.mode = 0o600
            member.size = len(payload)
            member.mtime = int(time.time())
            tar.addfile(member, io.BytesIO(payload))
        # The fixture producer installs one closed file with the runtime owner's UID.
        docker('cp', '-a', '-', name + ':' + staging, data=archive.getvalue())
        ready, headers = operation(307, operation='commit', resourceId=lease['resourceId'],
                                   generation=lease['generation'], entrypoint='artifact.ts')
        assert ready['state'] == 'ready'
        assert headers['location'] == ready['url']
        return ready

    try:
        docker('create', '--name', name, '--platform', platform,
               '-p', '127.0.0.1::9090', '-v', str(data_dir) + ':/app/data',
               '-v', str(config) + ':/app/config.conf:ro',
               '-v', str(pool) + ':/app/node_pool.json:ro', runtime)
        created = True
        docker('start', name)
        wait_ready()
        status, _, body = request('POST', '/api/v1/rules/origin-http-test', chain)
        assert status in (200, 201), (status, body)
        operation(404, operation='resolve', resourceId='0' * 64)
        stable = publish('restart-preserved', 600000)
        location = stable['url']
        status, headers, body = request('GET', location)
        assert status == 200 and body == payload
        modified = headers['last-modified']
        status, headers, body = request('GET', location, headers={'Range': 'bytes=0-15'})
        assert status == 206 and body == payload[:16]
        assert headers['content-range'] == 'bytes 0-15/' + str(len(payload))
        assert request('GET', location, headers={'Range': 'bytes=999999999-'})[0] == 416
        assert request('GET', location, headers={'If-Modified-Since': modified})[0] == 304
        operation(404, operation='resolve', resourceId=stable['resourceId'], member='missing.ts')
        docker('restart', name)
        wait_ready()
        resolved, _ = operation(307, operation='resolve', resourceId=stable['resourceId'])
        assert resolved['url'] == location
        assert request('GET', location)[2] == payload
        parent = publish('expiry-parent', 10000)
        child = publish('expiry-child', 60000, parent['resourceId'])
        assert child['expiresAt'] == parent['expiresAt']
        # Native static GET does not run an origin sweep: this checks autonomous expiry.
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            if all(request('GET', r['url'])[0] == 404 for r in (parent, child)):
                break
            time.sleep(0.1)
        else:
            raise AssertionError('expired static resources remain accessible')
        for resource in (parent, child):
            operation(410, operation='resolve', resourceId=resource['resourceId'])
        assert request('GET', location)[2] == payload
        # Verify physical trash removal after healthy expiry, independently of REST state.
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            archive = docker('cp', name + ':/app/data/resource-origin/trash', '-')
            with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
                members = tar.getmembers()
                assert any(m.name.rstrip('/') == 'trash' and m.isdir() for m in members)
                members = [m for m in members if m.name.rstrip('/') != 'trash']
            if not members:
                break
            time.sleep(0.1)
        else:
            raise AssertionError('healthy expiry left trash entries')
        receipt = {'runtime': runtime, 'platform': platform, 'pluginSha256': digest,
                   'checks': ['202/307/404/410', 'GET/206/416/304', 'parent-child expiry',
                              'autonomous expiry', 'trash removal', 'restart preservation']}
        (work / 'receipt.json').write_text(json.dumps(receipt, indent=2) + '\n')
        print(json.dumps(receipt, indent=2))
    finally:
        if created:
            try:
                logs = subprocess.run(['docker', 'logs', name], stdout=subprocess.PIPE,
                                      stderr=subprocess.STDOUT, timeout=30)
                (work / 'runtime.log').write_bytes(logs.stdout)
            finally:
                docker('rm', '-f', name)
        print('Integration evidence:', work)


if __name__ == '__main__':
    main()
