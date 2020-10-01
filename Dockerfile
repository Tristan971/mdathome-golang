FROM fedora:33 as builder

RUN dnf makecache \
  && dnf install -y \
    golang \
    make \
    upx

RUN dnf groupinstall -y "Development Tools"

ADD . /mangahome
WORKDIR /mangahome

RUN make

FROM fedora:33

RUN dnf makecache && dnf install -y dumb-init && dnf clean all

WORKDIR /mangahome
COPY --from=builder /mangahome/mdathome-golang /mangahome/mdathome-golang

ENTRYPOINT ["dumb-init", "--rewrite", "15:2", "--"]
CMD ["/mangahome/mdathome-golang"]
