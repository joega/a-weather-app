"""Explicit, user-only application launcher installation; never overwrite files."""
import os
from pathlib import Path
import secrets
import shutil
import stat

from weather.files import read_file

ROOT = Path(__file__).resolve().parents[1]


def directory(path):
    if not path.is_absolute() or '..' in path.parts:
        raise ValueError('absolute data directory required')
    fd = os.open('/', os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    try:
        for part in path.parts[1:]:
            try:
                os.mkdir(part, 0o755, dir_fd=fd)
            except FileExistsError:
                pass
            child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
            os.close(fd)
            fd = child
            info = os.fstat(fd)
            if not info.st_mode & stat.S_ISVTX and (info.st_uid not in (0, os.geteuid()) or info.st_mode & 0o022):
                raise PermissionError('unsafe directory')
        if os.fstat(fd).st_uid != os.geteuid():
            raise PermissionError('user directory required')
        return fd
    except BaseException:
        os.close(fd)
        raise


def publish(fd, name, data, alternatives=()):
    """Fully write a private temporary inode, then publish without replacement."""
    try:
        existing = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=fd)
    except FileNotFoundError:
        existing = None
    if existing is not None:
        try:
            info = os.fstat(existing)
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or info.st_nlink != 1 or info.st_mode & 0o022 or info.st_size > 65536:
                raise FileExistsError('conflicting launcher file')
            value = os.read(existing, 65537)
            if value not in (data, *alternatives):
                raise FileExistsError('conflicting launcher file')
            return
        finally:
            os.close(existing)
    temporary = '.a-weather-app-' + secrets.token_hex(16)
    out = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=fd)
    try:
        with os.fdopen(out, 'wb') as stream:
            stream.write(data)
            stream.flush()
            os.fchmod(stream.fileno(), 0o644)
            os.fsync(stream.fileno())
        os.link(temporary, name, src_dir_fd=fd, dst_dir_fd=fd, follow_symlinks=False)
    finally:
        os.unlink(temporary, dir_fd=fd)
    os.fsync(fd)


def install():
    try:
        data = Path(os.environ.get('XDG_DATA_HOME') or str(Path.home() / '.local/share'))
        executable = str(ROOT / 'a-weather-app')
        # Keep the executable a single quoted Desktop Entry argument. Reject
        # field codes and characters with inconsistent launcher escaping rules.
        if any(ord(c) < 32 or ord(c) == 127 or c in '\\"`$%=' for c in str(data) + executable):
            return 'unsupported_path'
        quoted = executable
        template = read_file(ROOT / 'packaging/a-weather-app.desktop', 8192)
        icon_path = str(data / 'icons/hicolor/scalable/apps/a-weather-app.svg').replace('\\', '\\\\')
        desktop = '\n'.join('Exec="' + quoted + '"' if line.startswith('Exec=') else
                            'Icon=' + icon_path if line.startswith('Icon=') else line
                            for line in template.decode().splitlines() if not line.startswith('TryExec=')) + '\n'
        alternatives = ()
        current = shutil.which('a-weather-app')
        if current and Path(current).resolve() == ROOT / 'a-weather-app':
            alternatives = (template,)
        icon = read_file(ROOT / 'packaging/icons/a-weather-app.svg', 65536)
        for folder, name, content, accepted in (
            (data / 'icons/hicolor/scalable/apps', 'a-weather-app.svg', icon, ()),
            (data / 'applications', 'a-weather-app.desktop', desktop.encode(), alternatives),
        ):
            fd = directory(folder)
            try:
                publish(fd, name, content, accepted)
            finally:
                os.close(fd)
        return 'installed'
    except FileExistsError:
        return 'conflict'
    except (OSError, ValueError):
        return 'failed'
