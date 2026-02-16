// resolv_compat_linux.c
//
// The ntgcalls shared library is built with Chromium's bundled clang toolchain,
// which links against glibc internal symbols (__dn_expand, __res_nquery) instead
// of the public resolv API. Older glibc versions (e.g. Ubuntu 22.04) don't export
// these __ prefixed symbols. Provide thin wrappers that forward to the public API.

#ifdef __linux__

#include <resolv.h>

// __dn_expand is an internal glibc alias for dn_expand.
int __dn_expand(const unsigned char *msg, const unsigned char *eom,
                const unsigned char *src, char *dst, int dstsiz) {
    return dn_expand(msg, eom, src, dst, dstsiz);
}

// __res_nquery is an internal glibc alias for res_nquery.
int __res_nquery(res_state statp, const char *dname, int class, int type,
                 unsigned char *answer, int anslen) {
    return res_nquery(statp, dname, class, type, answer, anslen);
}

#endif
