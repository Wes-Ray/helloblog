# Goal
A basic golang server to host a blog with comments, likes, and images that are managed by golang, sqlite, and htmx to maintain serve that.

# Running
## localhost
### Init Run
Init session key with openssl
`openssl rand -base64 32`
`export SESSION_KEY="replace-with-32-length-random-string"`
`go run main.go`

### Normal Run
`go run main.go`

## Remote (VPS)
### Utility
Read log (auto updates)
tail -f blog.log

### Initial setup
Init session key with openssl
`openssl rand -base64 32`

Create  
'/etc/systemd/system/helloblog.service'
```
[Unit]
Description=Hello Blog
After=network.target

[Service]
ExecStart=/home/user/blog/helloblog
Environment="SESSION_KEY=replace-with-32-length-random-string"
Environment="ADMIN_USERNAME=replace-with-admin-username"
Environment="ADMIN_PASSWORD=replace-with-admin-password"
Restart=always
User=user
Group=user
Environment=PATH=/usr/bin:/usr/local/bin
WorkingDirectory=/home/user/blog/

[Install]
WantedBy=multi-user.target
```

Restart systemd and start the service  
`sudo systemctl daemon-reload`  
`sudo systemctl start helloblog.service`  
`sudo systemctl status helloblog.service`  

Add custom no password entry for specific sudo commands for update_server  
`sudo visudo`
`user ALL=(ALL) NOPASSWD: /usr/bin/systemctl restart helloblog.service, /usr/bin/systemctl is-active blog.service --quiet helloblog.service`

Setup nginx to redirect port 8080 IPv4 to domain name
`sudo apt update`
`sudo apt install nginx`
`sudo vim /etc/nginx/sites-available/yourdomain.com`
```
server {
    listen 80;
    server_name yourdomain.com www.yourdomain.com;

    client_max_body_size 100M;  # max upload size
    
    location / {
        proxy_pass http://localhost:LOCALPORT;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```
`sudo ln -s /etc/nginx/sites-available/yourdomain.com /etc/nginx/sites-enabled/`
`sudo nginx -t`
`sudo systemctl restart nginx`

Open firewall for 443 and 80
`sudo ufw allow http`
`sudo ufw allow https`
`sudo ufw enable`
`sudo ufw status`

Setup SSL/HTTPS with Let's Encrypt
- Make sure you have both domain and www.domain pointed at server IP
`sudo apt install certbot python3-certbot-nginx`
`sudo certbot --nginx -d yourdomain.com -d www.yourdomain.com`


### Update Remote
`./update_server.sh`
