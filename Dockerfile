FROM golang:1.16
ADD . / /build/
WORKDIR /build
RUN CGO_ENABLED=0 GOOS=linux go build -a -o ipmi_exporter .

FROM alpine:latest  
RUN apk --no-cache add ipmitool
WORKDIR /root/
COPY --from=0 /build/ipmi_exporter ./
CMD ["./ipmi_exporter"]  
