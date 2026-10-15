FROM fedora:41

RUN dnf install -y \
    bluez \
    bluez-libs \
    dbus \
    opus-tools \
    ffmpeg-free \
    gcc \
    git \
    go \
    && dnf clean all

COPY --from=builder /usr/local/bin/aplaud /usr/local/bin/aplaud

# Copy whisper.cpp libraries if available
# COPY --from=whisper-builder /usr/local/lib/libwhisper.so /usr/local/lib/
# COPY --from=whisper-builder /usr/local/lib/libggml.so /usr/local/lib/

WORKDIR /root

ENTRYPOINT ["aplaud"]
