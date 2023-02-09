#!/bin/bash 

for i in {0..100}
do
echo $i
echo "run lab2A"
go test -race -run 2A >> lab2A;
echo "run lab2B"
go test -race -run 2B >> lab2B;
echo "run lab2C"
go test -race -run 2C >> lab2C;
done