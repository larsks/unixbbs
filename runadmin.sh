docker run --rm -it \
  --hostname bbs.local \
  -v unixbbs-data:/bbs-data \
  -v unixbbs-sock:/bbs-sock \
  -v unixbbs-mailsock:/bbs-sock/mail \
  unixbbs-admin
