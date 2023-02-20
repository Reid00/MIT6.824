#!/bin/bash 

for i in {0..100}
do
echo $i
echo "run lab3A"
go test -race -run 3A >> lab3A ;
echo "run lab3B"
go test -race -run 3B >> lab3B;
done