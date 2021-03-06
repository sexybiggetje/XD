#!/usr/bin/env bash

version="$1"
git clean -xdf
make assets
GOOS=windows GOARCH=386 make clean build && mv XD.exe XD-$version-win32.exe && gpg --sign --detach XD-$version-win32.exe
GOOS=windows GOARCH=amd64 make clean build && mv XD.exe XD-$version-win64.exe && gpg --sign --detach XD-$version-win64.exe
GOOS=darwin GOARCH=amd64 make clean build && mv XD XD-$version-darwin && gpg --sign --detach XD-$version-darwin
GOOS=linux GOARCH=386 make clean build && mv XD XD-$version-linux-i386 && gpg --sign --detach XD-$version-linux-i386
GOOS=linux GOARCH=amd64 make clean build && mv XD XD-$version-linux-amd64 && gpg --sign --detach XD-$version-linux-amd64
GOOS=linux GOARCH=arm make clean build && mv XD XD-$version-linux-arm && gpg --sign --detach XD-$version-linux-arm
GOOS=linux GOARCH=arm GOARM=6 make clean build && mv XD XD-$version-linux-rpi && gpg --sign --detach XD-$version-linux-rpi
GOOS=linux GOARCH=ppc64 make clean build && mv XD XD-$version-linux-ppc64 && gpg --sign --detach XD-$version-linux-ppc64
GOOS=freebsd GOARCH=amd64 make clean build  && mv XD XD-$version-freebsd-amd64 && gpg --sign --detach XD-$version-freebsd-amd64
