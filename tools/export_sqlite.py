#!/usr/bin/env python3
"""Offline, read-only SQLite export. Runtime and Go dependencies remain database-free."""
import argparse
import base64
import fcntl
import json
import os
from pathlib import Path
import secrets
import shutil
import sqlite3
from datetime import datetime, timezone


def sync_write(path, data):
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
        json.dump(data, handle, ensure_ascii=False, indent=2)
        handle.write('\n')
        handle.flush()
        os.fsync(handle.fileno())
    descriptor = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def main():
    parser = argparse.ArgumentParser(description='离线备份并导出 v2way SQLite 数据，不改变原库')
    parser.add_argument('--data', default='.data')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    root = Path(args.data).resolve()
    output = Path(args.output).absolute()
    if output.exists():
        raise SystemExit('导出文件已存在，拒绝覆盖。')
    with open(root / 'instance.lock', 'a+b') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise SystemExit('实例仍在运行，请先停止后端。')
        source = root / 'gateway.db'
        if not source.is_file():
            raise SystemExit('没有找到旧数据库。')
        backup = root / 'backups' / ('sqlite-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ') + '-' + secrets.token_hex(4))
        backup.mkdir(parents=True, mode=0o700)
        os.chmod(backup.parent, 0o700)
        old = sqlite3.connect(source.as_uri() + '?mode=ro', uri=True)
        copy = sqlite3.connect(backup / 'gateway.db')
        old.backup(copy)
        old.close()
        os.chmod(backup / 'gateway.db', 0o600)
        copy.row_factory = sqlite3.Row
        for entry in root.iterdir():
            if entry.name in {'backups', 'instance.lock', 'gateway.db', 'gateway.db-wal', 'gateway.db-shm'} or entry == output:
                continue
            if entry.is_symlink():
                raise SystemExit('备份遇到符号链接，已停止，请检查数据目录。')
            if entry.is_dir():
                if any(path.is_symlink() for path in entry.rglob('*')):
                    raise SystemExit('备份目录包含符号链接，已停止。')
                shutil.copytree(entry, backup / entry.name)
            else:
                shutil.copy2(entry, backup / entry.name)
        for path in backup.rglob('*'):
            os.chmod(path, 0o700 if path.is_dir() else 0o600)
        schema = copy.execute("SELECT value FROM metadata WHERE key='schema_version'").fetchone()
        if schema is None or schema[0] != '1':
            raise SystemExit('旧数据库格式不受支持。')
        accounts = copy.execute('SELECT username,password FROM account').fetchall()
        if len(accounts) > 1:
            raise SystemExit('旧库包含多个管理员，拒绝自动选择。')
        draft = copy.execute('SELECT revision,base_version,config FROM draft WHERE id=1').fetchone()
        if draft is None:
            raise SystemExit('旧草稿缺失。')
        active = int(copy.execute("SELECT value FROM metadata WHERE key='active_version'").fetchone()[0])
        newest = copy.execute("SELECT COALESCE(MAX(id),0) FROM versions WHERE state='succeeded'").fetchone()[0]
        if active != newest:
            raise SystemExit('运行版本与成功版本记录不一致，请先人工核对旧库。')
        versions = []
        for row in copy.execute("SELECT id,created_at,note,config,request_id FROM versions WHERE state='succeeded' AND id<=? ORDER BY id DESC LIMIT 101", (active,)):
            versions.append({'id': row['id'], 'createdAt': row['created_at'], 'note': row['note'], 'config': json.loads(row['config']), 'requestId': row['request_id']})
        certificates = []
        if copy.execute("SELECT 1 FROM sqlite_master WHERE name='certificates'").fetchone():
            for row in copy.execute('SELECT id,info,secret FROM certificates'):
                certificates.append({'id': row['id'], 'info': json.loads(row['info']), 'secret': base64.b64encode(row['secret']).decode('ascii')})
        audits = [{'id': row['id'], 'time': row['time'], 'action': row['action'], 'detail': row['detail']} for row in copy.execute('SELECT id,time,action,detail FROM audit ORDER BY id')]
        account = {'username': accounts[0]['username'], 'passwordHash': accounts[0]['password']} if accounts else None
        bundle = {'schemaVersion': 1, 'activeVersion': active, 'account': account, 'draft': {'schemaVersion': 1, 'revision': draft['revision'], 'baseVersion': draft['base_version'], 'config': json.loads(draft['config'])}, 'versions': versions, 'audits': audits, 'certificates': certificates}
        copy.close()
        output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        sync_write(output, bundle)
        for path in backup.rglob('*'):
            if path.is_file():
                with open(path, 'rb') as handle:
                    os.fsync(handle.fileno())
        for directory in [p for p in backup.rglob('*') if p.is_dir()] + [backup, backup.parent, root]:
            descriptor = os.open(directory, os.O_RDONLY)
            try:
                os.fsync(descriptor)
            finally:
                os.close(descriptor)
        print('离线备份：', backup)
        print('导出文件：', output)
        print(f'管理员 {len(accounts)} 个，历史版本 {len(versions)} 个，旧证书 {len(certificates)} 张；没有导出登录会话或打印秘密。')


if __name__ == '__main__':
    main()
