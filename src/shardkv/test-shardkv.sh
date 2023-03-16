#!/bin/bash 

for i in {0..1000}
do
echo $i
echo "run lab4B"
go test -race >> lab4B_2 ;
done

# Bug DEBUG 
# for i in {0..1000}
# do
# echo $i
# echo "run lab4B"
# go test -race -v -run TestMissChange >> lab4B_2 ;
# done