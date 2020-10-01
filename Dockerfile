FROM fedora:33

RUN dnf makecache \
  && dnf install -y \
    dumb-init \
    htop \
    procps-ng \
    wget \
  && dnf clean all

ARG RELEASE

WORKDIR /mangahome
RUN curl -Sso mdgo https://github.com/lflare/mdathome-golang/releases/download/v${RELEASE}/mdathome-${RELEASE}-linux_amd64
RUN chmod -v +x mdgo

ENTRYPOINT [ "dumb-init", "--" ]
CMD [ "/mangahome/mdgo" ]
