#!/bin/bash 

for i in {0..100}
do
echo $i
echo "run lab4A"
go test -race >> lab4A ;
done