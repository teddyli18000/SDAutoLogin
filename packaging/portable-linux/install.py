#!/usr/bin/env python3
"""Install the portable SDAutoLogin folder and its per-user systemd unit."""

from __future__ import print_function

import argparse
import filecmp
import os
import shutil
import stat
import subprocess
import sys
import tempfile
from pathlib import Path


APP_NAME = "SDAutoLogin"
UNIT_NAME = "sdautologin.service"
CONFIG_TEMPLATE = ".startup/config.json.in"
HELPER_FILES = ("run-with-panel.py", "open-panel.py")
INSTALL_DIRS = (
    ".startup",
    "logs",
    "logs/config",
    "logs/data",
    "logs/state",
    "logs/cache",
    "logs/runtime",
    "logs/tmp",
)
CONTROL_FILES = (
    ".startup/run-with-panel.py",
    ".startup/open-panel.py",
    ".startup/config.json.in",
    ".startup/sdautologin.service.in",
    ".startup/配置面板.desktop.in",
)


class InstallError(Exception):
    pass


def fail(message):
    raise InstallError(message)


def absolute_path(value):
    path = Path(os.path.abspath(os.path.expanduser(str(value))))
    text = str(path)
    if any(ord(char) < 32 or ord(char) == 127 for char in text):
        fail("路径含有控制字符，无法安全写入启动配置")
    if "%" in text:
        fail("路径包含 systemd 的 % 展开符，请换一个不含 % 的路径")
    return path


def desktop_directory(home):
    command = shutil.which("xdg-user-dir")
    if command:
        try:
            output = subprocess.check_output(
                [command, "DESKTOP"], stderr=subprocess.DEVNULL
            ).decode("utf-8").strip()
            if output and "\n" not in output and "\r" not in output:
                candidate = Path(output)
                if candidate.is_absolute():
                    return absolute_path(candidate)
        except (OSError, subprocess.CalledProcessError, UnicodeDecodeError):
            pass
    chinese = home / "桌面"
    if chinese.is_dir():
        return absolute_path(chinese)
    return absolute_path(home / "Desktop")


def path_exists(path):
    return os.path.lexists(str(path))


def check_directory(path, label):
    if path_exists(path) and (path.is_symlink() or not path.is_dir()):
        fail("{} 已存在但不是普通目录；为避免写入错误位置，安装已停止".format(label))


def check_file(path, label, preserve=False, executable=False):
    if not path_exists(path):
        return
    if path.is_symlink() or not path.is_file():
        fail("{} 已存在但不是普通文件；为避免覆盖链接或目录，安装已停止".format(label))
    if preserve and executable and not os.access(str(path), os.X_OK):
        fail("已安装的 SDAutoLogin 文件不可执行；安装未覆盖它，请先检查该文件")


def check_symlink(path, target, label):
    if not path_exists(path):
        return
    if not path.is_symlink():
        fail("{} 已存在且不是预期的符号链接；安装未修改它".format(label))
    if os.path.realpath(str(path)) != os.path.realpath(target):
        fail("{} 已指向其他位置；安装未修改它".format(label))


def check_directory_chain(path, label):
    current = Path(path.anchor)
    for part in path.parts[1:]:
        current = current / part
        if not path_exists(current):
            continue
        if current.is_symlink() or not current.is_dir():
            fail("{} 的目录链包含符号链接或非目录：{}".format(label, current))


def systemd_quote(value):
    value = str(value)
    if any(ord(char) < 32 or ord(char) == 127 for char in value) or "%" in value:
        fail("启动路径含有 systemd 不安全的字符")
    escaped = value.replace("\\", "\\\\").replace('"', '\\"').replace("$", "$$")
    return '"{}"'.format(escaped)


def desktop_quote(value):
    value = str(value)
    if any(ord(char) < 32 or ord(char) == 127 for char in value) or "%" in value:
        fail("启动路径含有桌面启动器不安全的字符")
    escaped = (
        value.replace("\\", "\\\\")
        .replace('"', '\\"')
        .replace("$", "\\$")
        .replace("`", "\\`")
    )
    return '"{}"'.format(escaped)


def render(template_path, values):
    try:
        body = template_path.read_text(encoding="utf-8")
    except OSError as exc:
        fail("无法读取安装模板 {}：{}".format(template_path.name, exc))
    for key, value in values.items():
        marker = "@@{}@@".format(key)
        if marker not in body:
            fail("安装模板缺少占位符 {}".format(marker))
        body = body.replace(marker, value)
    return body.encode("utf-8")


def make_private_directory(path):
    path.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(str(path), 0o700)


def write_atomic(path, data, mode):
    fd, name = tempfile.mkstemp(prefix="." + path.name + ".", dir=str(path.parent))
    temporary = Path(name)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, "wb") as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.replace(str(temporary), str(path))
    except Exception:
        try:
            os.close(fd)
        except OSError:
            pass
        try:
            temporary.unlink()
        except OSError:
            pass
        raise


def create_if_absent(source, destination, mode):
    flags = os.O_WRONLY | os.O_CREAT | os.O_EXCL
    fd = os.open(str(destination), flags, mode)
    try:
        os.fchmod(fd, mode)
        with os.fdopen(fd, "wb") as output:
            with source.open("rb") as input_file:
                shutil.copyfileobj(input_file, output)
            output.flush()
            os.fsync(output.fileno())
        return True
    except Exception:
        try:
            os.close(fd)
        except OSError:
            pass
        try:
            destination.unlink()
        except OSError:
            pass
        raise


def replace_from_source(source, destination, mode):
    fd, name = tempfile.mkstemp(
        prefix="." + destination.name + ".", dir=str(destination.parent)
    )
    os.close(fd)
    temporary = Path(name)
    try:
        shutil.copyfile(str(source), str(temporary))
        os.chmod(str(temporary), mode)
        os.replace(str(temporary), str(destination))
    except Exception:
        try:
            temporary.unlink()
        except OSError:
            pass
        raise


def check_source_files(package_root):
    app_source = package_root / APP_NAME
    required = [
        app_source / APP_NAME,
        package_root / "LICENSE",
        package_root / "README.zh-CN.md",
        package_root / "install.py",
    ]
    required.extend(app_source / name for name in CONTROL_FILES)
    for name in HELPER_FILES:
        required.append(app_source / ".startup" / name)
    for path in required:
        if not path.is_file() or path.is_symlink():
            fail("安装包缺少普通文件：{}".format(path.name))
    return app_source


def install(args):
    if sys.version_info < (3, 6):
        fail("便携版安装需要 Python 3.6 或更新版本")
    if os.geteuid() == 0:
        fail("请用当前桌面用户运行安装器，不要使用 sudo")

    package_root = Path(__file__).resolve().parent
    app_source = check_source_files(package_root)
    home = Path.home()
    if args.home:
        root = absolute_path(args.home)
    else:
        root = desktop_directory(home) / APP_NAME
        root = absolute_path(root)

    xdg_config = os.environ.get("XDG_CONFIG_HOME", "")
    if not xdg_config or not Path(os.path.expanduser(xdg_config)).is_absolute():
        xdg_config = str(home / ".config")
    systemd_user_dir = absolute_path(Path(xdg_config) / "systemd" / "user")
    wants_dir = systemd_user_dir / "default.target.wants"
    service_file = root / ".startup" / UNIT_NAME
    unit_link = systemd_user_dir / UNIT_NAME
    wants_link = wants_dir / UNIT_NAME

    python = absolute_path(sys.executable)
    runner = root / ".startup" / "run-with-panel.py"
    opener = root / ".startup" / "open-panel.py"
    service_bytes = render(
        app_source / ".startup" / "sdautologin.service.in",
        {"PYTHON": systemd_quote(python), "RUNNER": systemd_quote(runner)},
    )
    launcher_bytes = render(
        app_source / ".startup" / "配置面板.desktop.in",
        {"PYTHON": desktop_quote(python), "OPEN_PANEL": desktop_quote(opener)},
    )

    # Inspect every destination, including both external systemd links, before
    # creating directories or replacing any installed helper.
    check_directory(root, "安装目录")
    for relative in INSTALL_DIRS:
        check_directory(root / relative, "安装目录中的 " + relative)
    binary_destination = root / APP_NAME
    config_destination = root / "config.json"
    launcher_destination = root / "配置面板.desktop"
    check_file(binary_destination, "SDAutoLogin", preserve=True, executable=True)
    check_file(config_destination, "config.json", preserve=True)
    check_file(root / "LICENSE", "LICENSE")
    check_file(root / "README.zh-CN.md", "README.zh-CN.md")
    for relative in CONTROL_FILES:
        check_file(root / relative, relative)
    for name in HELPER_FILES:
        check_file(root / ".startup" / name, name)
    check_file(launcher_destination, "配置面板.desktop")
    check_file(root / "logs" / "runtime" / "settings-entry.url", "设置页一次性入口")
    check_directory_chain(systemd_user_dir, "systemd 用户单元")
    check_directory_chain(wants_dir, "systemd 启用目录")
    check_symlink(unit_link, str(service_file), "systemd 用户单元链接")
    check_symlink(wants_link, str(service_file), "systemd default.target 链接")

    systemctl = shutil.which("systemctl")
    if not systemctl:
        fail("找不到 systemctl；没有写入任何文件")

    binary_was_present = path_exists(binary_destination)
    config_was_present = path_exists(config_destination)
    binary_differs = binary_was_present and not filecmp.cmp(
        str(app_source / APP_NAME), str(binary_destination), shallow=False
    )
    if binary_differs and not args.replace_binary:
        fail(
            "已安装的 SDAutoLogin 与此压缩包中的二进制不同。为避免静默保留旧版本，"
            "请确认要升级后运行：python3 install.py --replace-binary"
        )
    if binary_differs and args.replace_binary:
        result = subprocess.call(
            [systemctl, "--user", "stop", UNIT_NAME],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        if result != 0:
            fail("无法停止现有 systemd --user 服务，尚未修改安装文件")

    make_private_directory(root)
    for relative in INSTALL_DIRS:
        make_private_directory(root / relative)

    if not path_exists(binary_destination):
        try:
            create_if_absent(app_source / APP_NAME, binary_destination, 0o755)
        except FileExistsError:
            check_file(binary_destination, "SDAutoLogin", preserve=True, executable=True)
    elif binary_differs:
        replace_from_source(app_source / APP_NAME, binary_destination, 0o755)
    for name in HELPER_FILES:
        source = app_source / ".startup" / name
        write_atomic(root / ".startup" / name, source.read_bytes(), 0o755)
    for relative in (".startup/config.json.in",):
        source = app_source / relative
        write_atomic(root / relative, source.read_bytes(), 0o600)
    for name in ("LICENSE", "README.zh-CN.md"):
        write_atomic(root / name, (package_root / name).read_bytes(), 0o644)
    write_atomic(service_file, service_bytes, 0o644)
    write_atomic(launcher_destination, launcher_bytes, 0o755)
    gio = shutil.which("gio")
    if gio:
        subprocess.call(
            [gio, "set", str(launcher_destination), "metadata::trusted", "true"],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

    if not path_exists(config_destination):
        try:
            create_if_absent(app_source / CONFIG_TEMPLATE, config_destination, 0o600)
        except FileExistsError:
            check_file(config_destination, "config.json", preserve=True)

    os.makedirs(str(systemd_user_dir), mode=0o700, exist_ok=True)
    os.makedirs(str(wants_dir), mode=0o700, exist_ok=True)
    if not path_exists(unit_link):
        os.symlink(str(service_file), str(unit_link))
    if not path_exists(wants_link):
        os.symlink(str(service_file), str(wants_link))

    for command in (
        [systemctl, "--user", "daemon-reload"],
        [systemctl, "--user", "enable", UNIT_NAME],
        [systemctl, "--user", "restart", UNIT_NAME],
    ):
        result = subprocess.call(
            command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL
        )
        if result != 0:
            fail(
                "文件已安装，但 systemd --user 命令失败（退出码 {}）。登录桌面会话后运行："
                "systemctl --user daemon-reload && systemctl --user enable --now {}".format(
                    result, UNIT_NAME
                )
            )

    print("已安装到：{}".format(root))
    print("设置入口：{}".format(root / "配置面板.desktop"))
    print("服务状态：systemctl --user status {}".format(UNIT_NAME))
    preserved = []
    if binary_was_present and not binary_differs:
        preserved.append("SDAutoLogin 二进制")
    if config_was_present:
        preserved.append("config.json")
    if preserved:
        print("已保留原有的{}。".format("和".join(preserved)))


def main():
    parser = argparse.ArgumentParser(
        description="将 SDAutoLogin 安装到桌面目录，并启用当前用户的 systemd 服务。"
    )
    parser.add_argument(
        "--home",
        metavar="PATH",
        help="安装根目录；省略时使用 xdg-user-dir DESKTOP/SDAutoLogin",
    )
    parser.add_argument(
        "--replace-binary",
        action="store_true",
        help="停止服务并替换已安装的不同版本二进制；config.json 仍会保留",
    )
    args = parser.parse_args()
    try:
        install(args)
    except InstallError as exc:
        print("安装失败：{}".format(exc), file=sys.stderr)
        return 1
    except (OSError, subprocess.SubprocessError) as exc:
        print("安装失败：{}".format(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
