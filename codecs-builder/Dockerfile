# syntax=docker/dockerfile:1.7
#
# codecs-builder builds x264, SVT-AV1, libopus, libvpx, libzimg from
# source, plus a stripped FFmpeg n7.1.3 with the common encoder set.
#
# The resulting `:latest` image is consumed by transcode-worker-node's
# vendor-specific runtime stages so each operator-facing image gets a
# consistent codec set. Vendor-specific (`runtime-nvidia` etc.) layer
# the relevant hwaccel encoders on top.

FROM ubuntu:22.04 AS codecs-builder

ARG FFMPEG_VERSION=n7.1.3
ARG SVT_AV1_VERSION=v2.1.0
ARG X264_VERSION=stable
ARG LIBOPUS_VERSION=1.5.2
ARG LIBVPX_VERSION=v1.14.1
ARG LIBZIMG_VERSION=release-3.0.5

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y --no-install-recommends \
        build-essential cmake nasm yasm git pkg-config \
        autoconf automake libtool meson ninja-build \
        ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# x264
RUN git clone --depth=1 --branch ${X264_VERSION} https://code.videolan.org/videolan/x264.git && \
    cd x264 && ./configure --enable-static --enable-pic --prefix=/usr/local && \
    make -j$(nproc) && make install

# SVT-AV1
RUN git clone --depth=1 --branch ${SVT_AV1_VERSION} https://gitlab.com/AOMediaCodec/SVT-AV1.git && \
    cd SVT-AV1 && cmake -S . -B build -DCMAKE_INSTALL_PREFIX=/usr/local -DBUILD_SHARED_LIBS=OFF && \
    cmake --build build -j$(nproc) && cmake --install build

# libopus
RUN curl -fLO https://github.com/xiph/opus/releases/download/v${LIBOPUS_VERSION}/opus-${LIBOPUS_VERSION}.tar.gz && \
    tar xf opus-${LIBOPUS_VERSION}.tar.gz && cd opus-${LIBOPUS_VERSION} && \
    ./configure --enable-static --disable-shared --prefix=/usr/local && \
    make -j$(nproc) && make install

# libvpx
RUN git clone --depth=1 --branch ${LIBVPX_VERSION} https://chromium.googlesource.com/webm/libvpx && \
    cd libvpx && ./configure --enable-static --disable-shared --prefix=/usr/local --disable-examples --disable-tools --disable-docs && \
    make -j$(nproc) && make install

# libzimg
RUN git clone --depth=1 --branch ${LIBZIMG_VERSION} https://github.com/sekrit-twc/zimg.git && \
    cd zimg && ./autogen.sh && ./configure --enable-static --disable-shared --prefix=/usr/local && \
    make -j$(nproc) && make install

# FFmpeg base build (no hwaccel; vendor-specific stages add it)
RUN git clone --depth=1 --branch ${FFMPEG_VERSION} https://git.ffmpeg.org/ffmpeg.git && \
    cd ffmpeg && \
    ./configure \
        --prefix=/usr/local \
        --enable-gpl --enable-nonfree \
        --enable-libx264 --enable-libsvtav1 --enable-libopus --enable-libvpx \
        --enable-libzimg \
        --pkg-config-flags=--static \
        --extra-cflags="-I/usr/local/include" \
        --extra-ldflags="-L/usr/local/lib" && \
    make -j$(nproc) && make install
