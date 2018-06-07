These notes are how to build telegraf from the IT Innovation fork.


Install go

```
wget https://dl.google.com/go/go1.10.2.linux-amd64.tar.gz
tar -C /usr/local -xzf go1.10.2.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin'  >> /etc/profile
echo 'export GOPATH=/vagrant' >>  ~/.bash_profile
```

Install make (required by telegraf)

```
apt-get install make
```

Clone the telegraf repo

```
go get -d github.com/influxdata/telegraf
cd $GOPATH/src/github.com/influxdata/telegraf
```

Add the itinnov fork as a remote

```
git remote add itinnov https://github.com/it-innovation/telegraf.git
git pull itinnov master
```

Push changes to the itinnov fork

```
git push itinnov
```

Build telegraf

```
make install
```

Run the unit tests

```
make test
```

Create the packages

```
make package
```

