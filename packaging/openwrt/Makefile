# This is an OpenWrt package, for a firmware image built from the OpenWrt source
# tree or the SDK. It is not part of this repository's own build: the .ipk that
# tools/build makes is the same program, packaged for a router that already runs
# OpenWrt, while this Makefile is for an image that carries the program from the
# start.
#
# To use it, put the directory in the tree under the name of the package, which
# is the name the build system looks it up by:
#
#   mkdir -p package/sdautologin
#   cp -r packaging/openwrt/. package/sdautologin/
#   make menuconfig          # Network -> sdautologin
#
# It installs the program as it is, not compressed: an image is stored in a
# squashfs, which compresses everything it holds, and it does that better than
# gzip does (about 1.9 MiB instead of 2.7 for this program). Compressing it here
# would add a megabyte to the image and cost the program its place in the read
# only filesystem, for nothing.

include $(TOPDIR)/rules.mk

PKG_NAME:=sdautologin
# The version of the program, which is the tag the sources are released at.
# PKG_SOURCE_VERSION below is the git ref this fetches, so the number has to be
# written here rather than read out of the sources it fetches. A test in
# tools/build compares it with the Version variable in internal/app/payload.go,
# so the two cannot drift apart without a build failing.
PKG_VERSION:=1.1.0
PKG_RELEASE:=1

PKG_SOURCE_PROTO:=git
PKG_SOURCE_URL:=https://git.zqlhub.com/zqlan/SDAutoLogin.git
PKG_SOURCE_VERSION:=v$(PKG_VERSION)
# The sources are taken from the repository above rather than from a mirror, so
# there is no tarball of theirs to check a hash against.
PKG_MIRROR_HASH:=skip

PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE
PKG_MAINTAINER:=Zhiqiang Lan <zqlan@suda.edu.cn>

# Go is cross compiled by a host build of the toolchain, and the program is one
# binary with nothing to link against at the end.
PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
# Go does not support the mips16 instruction set, and a build that is compiled
# for it fails to link.
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=sdautologin
GO_PKG_BUILD_PKG:=sdautologin/cmd/SDAutoLogin
# The version the program reports is written into it by the linker, and this
# build does not go through tools/build, which is where that happens. PKG_VERSION
# is the same number as the tag it fetches, so it is passed on here; a build that
# did not get it would be one that cannot say which release it is.
GO_PKG_LDFLAGS:=-s -w -X sdautologin/internal/app.Version=$(PKG_VERSION)
# The tray is left out: a router has no desktop session to put an icon in, and
# it is the largest thing the program can do without.
GO_PKG_TAGS:=notray

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

# Go builds for some architectures and not others, and this is the list of the
# ones it does; DEPENDS below is set to that list.
define Package/sdautologin
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Campus network auto login
  URL:=https://git.zqlhub.com/zqlan/SDAutoLogin
  DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Package/sdautologin/description
 Keeps a Dr.COM ePortal session alive on a router, checking it every few
 seconds and signing back in as soon as the session is gone.
endef

# The account, as UCI settings are kept on a router. Declaring it here is what
# makes an upgrade keep what is in it.
define Package/sdautologin/conffiles
/etc/config/sdautologin
endef

# A package that is installed and does not come up is of no use, so the service
# is enabled and started. The same two lines are in the postinst of the .ipk,
# which adds the instructions a person installing it by hand needs.
define Package/sdautologin/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || {
	/etc/init.d/sdautologin enable
	/etc/init.d/sdautologin start
}
endef

# The other way round on the way out: what a removal leaves behind is a process
# nothing on the router can reach any more, since what runs is the copy unpacked
# into /tmp. The prerm of the .ipk is the same line, with the reason written out.
define Package/sdautologin/prerm
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] || /etc/init.d/sdautologin stop
endef

# The init script and the configuration are the two files the .ipk installs as
# well, taken from the same place, so the two packages cannot drift.
define Package/sdautologin/install
	$(call GoPackage/Package/Install/Bin,$(PKG_INSTALL_DIR))

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_INSTALL_DIR)/usr/bin/SDAutoLogin $(1)/usr/bin/SDAutoLogin

	$(INSTALL_DIR) $(1)/etc/init.d $(1)/etc/config
	$(INSTALL_BIN) ./root/etc/init.d/sdautologin $(1)/etc/init.d/sdautologin
	$(INSTALL_CONF) ./root/etc/config/sdautologin $(1)/etc/config/sdautologin
endef

$(eval $(call GoBinPackage,sdautologin))
$(eval $(call BuildPackage,sdautologin))
